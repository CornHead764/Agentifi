package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/httpx"
	"github.com/CornHead764/agentifi/backend/internal/textutil"
)

// A car's Kelley Blue Book value, through the GraphQL endpoint KBB's own
// "What's my car worth" page calls: the VIN decodes to a year, make and model,
// then to a vehicle with its factory options, which is priced for a ZIP code
// and a mileage. The value recorded is the private-party value in good
// condition, what the page shows as "Sell it yourself".

const (
	kbbOrigin   = "https://www.kbb.com"
	kbbDocument = "/whats-my-car-worth/"
	kbbGraphQL  = kbbOrigin + "/api/owners-argo/"

	kbbCondition   = "good"
	kbbRecordedKey = "PP_Good"
	kbbTradeInKey  = "TradeIn"
	kbbAnswerLimit = 4 << 20
)

type KelleyBlueBook struct {
	Browser ValuationBrowser
}

func (k *KelleyBlueBook) Name() string         { return "kbb" }
func (k *KelleyBlueBook) AssetTypes() []string { return []string{AssetTypeVehicle} }
func (k *KelleyBlueBook) IsConfigured() bool   { return k.Browser != nil }

func (k *KelleyBlueBook) Estimate(ctx context.Context, subject ValuationSubject) (*ValuationQuote, error) {
	vin := strings.ToUpper(strings.TrimSpace(subject.VIN))
	if vin == "" {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, lookupTimeout)
	defer cancel()

	surface, err := k.Browser(kbbOrigin, kbbDocument)
	if err != nil {
		return nil, err
	}
	defer closeSurface(surface)

	zip, err := kbbZip(ctx, surface.Page)
	if err != nil {
		return nil, err
	}
	if zip == "" {
		slog.Info("kbb placed this server in no ZIP code")
		return nil, nil
	}
	return kbbValue(ctx, lookupFetcher(surface, kbbOrigin, kbbDocument), vin, subject.Mileage, zip)
}

// kbbZipCookie is the ZIP code KBB places the visitor in from their IP address,
// kept where the page's scripts can read it. Values are regional, and a
// self-hosted server sits at its owner's home.
var kbbZipCookie = regexp.MustCompile(`(?:^|;\s*)ZipCode=(\d{5})\b`)

func kbbZip(ctx context.Context, page browser.Page) (string, error) {
	var cookie string
	if err := browser.EvaluateIntoContext(ctx, page, `() => document.cookie`, nil, &cookie); err != nil {
		return "", fmt.Errorf("kbb: reading the page's ZIP code: %w", err)
	}
	match := kbbZipCookie.FindStringSubmatch(cookie)
	if match == nil {
		return "", nil
	}
	return match[1], nil
}

const (
	kbbDecodeQuery = `query($vin: String){ v: vehicleUrlByVinCads(vin: $vin){ url error make model year } }`

	kbbVehicleQuery = `query($year: String, $make: String, $model: String, $vin: String){ ` +
		`ymm: vinLicensePageCads(vehicleClass: "UsedCar", year: $year, make: $make, model: $model, vin: $vin){ ` +
		`defaultVehicleId typicalMileage bodyStyles{ name trims{ name vehicleId } } } }`

	kbbOptionsQuery = `query($vin: String, $id: Int){ ` +
		`d: vinLicenseVehicleDetailsCads(vin: $vin, selectedVehicleId: $id){ ` +
		`selectedVehicle{ vehicleId vehicleOptionIds } trims{ vehicleId trimName } } }`

	kbbPriceQuery = `query($zip: String!, $id: String!, $opts: String, $cond: String, $miles: String!){ ` +
		`p: priceAdvisorCarousel(zipcode: $zip, vehicleId: $id, selectHistory: $opts, condition: $cond, mileage: $miles){ ` +
		`privateParty{ Data{ APIData{ vehicle{ values{ type value low high } } } } } ` +
		`tradeIn{ Data{ APIData{ vehicle{ values{ type value low high } } } } } } }`
)

func kbbValue(ctx context.Context, fetcher browser.Fetcher, vin string, mileage int, zip string) (*ValuationQuote, error) {
	var decoded struct {
		V *struct {
			Error json.RawMessage `json:"error"`
			Make  kbbText         `json:"make"`
			Model kbbText         `json:"model"`
			Year  kbbText         `json:"year"`
		} `json:"v"`
	}
	if err := kbbQuery(ctx, fetcher, kbbDecodeQuery, map[string]any{"vin": vin}, &decoded); err != nil {
		return nil, err
	}
	car := decoded.V
	if car == nil || kbbSet(car.Error) || car.Make == "" || car.Model == "" || car.Year == "" {
		slog.Info("kbb could not decode the VIN")
		return nil, nil
	}
	lowerVIN := strings.ToLower(vin)

	var vehicle struct {
		YMM *struct {
			DefaultVehicleID kbbText `json:"defaultVehicleId"`
			TypicalMileage   kbbText `json:"typicalMileage"`
		} `json:"ymm"`
	}
	if err := kbbQuery(ctx, fetcher, kbbVehicleQuery, map[string]any{
		"year": string(car.Year), "make": strings.ToLower(string(car.Make)),
		"model": strings.ToLower(string(car.Model)), "vin": lowerVIN,
	}, &vehicle); err != nil {
		return nil, err
	}
	if vehicle.YMM == nil {
		slog.Info("kbb has no vehicle for the decoded VIN", "year", car.Year, "make", car.Make, "model", car.Model)
		return nil, nil
	}
	defaultID, err := strconv.Atoi(string(vehicle.YMM.DefaultVehicleID))
	if err != nil {
		slog.Info("kbb has no vehicle for the decoded VIN", "year", car.Year, "make", car.Make, "model", car.Model)
		return nil, nil
	}

	var details struct {
		D *struct {
			SelectedVehicle struct {
				VehicleID kbbText   `json:"vehicleId"`
				OptionIDs []kbbText `json:"vehicleOptionIds"`
			} `json:"selectedVehicle"`
			Trims []struct {
				VehicleID kbbText `json:"vehicleId"`
				TrimName  kbbText `json:"trimName"`
			} `json:"trims"`
		} `json:"d"`
	}
	if err := kbbQuery(ctx, fetcher, kbbOptionsQuery,
		map[string]any{"vin": lowerVIN, "id": defaultID}, &details); err != nil {
		return nil, err
	}
	if details.D == nil || details.D.SelectedVehicle.VehicleID == "" {
		slog.Info("kbb selected no vehicle for the VIN", "year", car.Year, "make", car.Make, "model", car.Model)
		return nil, nil
	}
	vehicleID := string(details.D.SelectedVehicle.VehicleID)
	trim := ""
	for _, candidate := range details.D.Trims {
		if candidate.VehicleID == details.D.SelectedVehicle.VehicleID {
			trim = string(candidate.TrimName)
		}
	}

	typical := mileage < 0
	if typical {
		mileage, err = strconv.Atoi(string(vehicle.YMM.TypicalMileage))
		if err != nil || mileage < 0 {
			slog.Info("no odometer reading and kbb gave no typical mileage")
			return nil, nil
		}
	}

	var priced struct {
		P *struct {
			PrivateParty kbbPrices `json:"privateParty"`
			TradeIn      kbbPrices `json:"tradeIn"`
		} `json:"p"`
	}
	if err := kbbQuery(ctx, fetcher, kbbPriceQuery, map[string]any{
		"zip": zip, "id": vehicleID, "opts": kbbSelectHistory(details.D.SelectedVehicle.OptionIDs),
		"cond": kbbCondition, "miles": strconv.Itoa(mileage),
	}, &priced); err != nil {
		return nil, err
	}
	if priced.P == nil {
		slog.Info("kbb priced nothing for the vehicle", "vehicle_id", vehicleID)
		return nil, nil
	}
	recorded, found := priced.P.PrivateParty.find(kbbRecordedKey)
	if !found {
		slog.Info("kbb gave no private-party value in good condition", "vehicle_id", vehicleID)
		return nil, nil
	}
	value, ok := valuationAmount(recorded.Value)
	if !ok {
		slog.Info("kbb's private-party value was not a positive amount", "vehicle_id", vehicleID)
		return nil, nil
	}

	pricedAs := PricedAs{
		Year: string(car.Year), Make: string(car.Make), Model: string(car.Model), Trim: trim,
		Mileage: mileage, HasMileage: true, TypicalMileage: typical,
	}
	low, hasLow := valuationAmount(recorded.Low)
	high, hasHigh := valuationAmount(recorded.High)
	if hasLow && hasHigh {
		pricedAs.Low, pricedAs.High, pricedAs.HasRange = low, high, true
	}
	detail := map[string]any{
		"kbb_vehicle_id": vehicleID,
		"zip":            zip,
		"condition":      kbbCondition,
	}
	if tradeIn, found := priced.P.TradeIn.find(kbbTradeInKey); found {
		detail["trade_in"] = detailNumber(tradeIn.Value)
	}
	return &ValuationQuote{Value: value, Currency: "USD", Priced: pricedAs, Detail: detail}, nil
}

// kbbSelectHistory is the options as the page sends them: each option id and
// whether it is chosen, all joined by bars.
func kbbSelectHistory(optionIDs []kbbText) string {
	parts := make([]string, 0, len(optionIDs))
	for _, id := range optionIDs {
		parts = append(parts, string(id)+"|true")
	}
	return strings.Join(parts, "|")
}

// kbbQuery posts one GraphQL query from the page and decodes its data into
// out. An error in the answer is the query no longer fitting KBB's schema, so
// it is raised rather than read as no estimate.
func kbbQuery(ctx context.Context, fetcher browser.Fetcher, query string, variables map[string]any, out any) error {
	body, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		return fmt.Errorf("kbb: encoding the query: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, kbbGraphQL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("kbb: request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := httpx.Read(fetcher, req, kbbAnswerLimit)
	if err != nil {
		return fmt.Errorf("kbb: the query failed: %w", err)
	}

	if resp.Status == http.StatusForbidden || resp.Status == http.StatusTooManyRequests {
		return fmt.Errorf("kbb: the query was refused (%d): %w", resp.Status, ErrValuationRateLimited)
	}
	if resp.Status >= 400 {
		return fmt.Errorf("kbb: the query answered %w", resp.Err())
	}
	var answer struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(resp.Body, &answer); err != nil {
		return fmt.Errorf("kbb: the answer is not JSON: %w", err)
	}
	if len(answer.Errors) > 0 {
		return fmt.Errorf("kbb: the query was rejected: %s", textutil.ClipMarked(answer.Errors[0].Message, httpx.ExcerptRunes))
	}
	if !kbbSet(answer.Data) {
		return nil
	}
	if err := json.Unmarshal(answer.Data, out); err != nil {
		return fmt.Errorf("kbb: the answer's data is not the expected shape: %w", err)
	}
	return nil
}

// kbbText is a field KBB answers as a string in one query and a number in the
// next (a vehicle id, a year), kept as its literal text.
type kbbText string

func (t *kbbText) UnmarshalJSON(raw []byte) error {
	if !kbbSet(raw) {
		*t = ""
		return nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		*t = kbbText(strings.TrimSpace(text))
		return nil
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err != nil {
		return err
	}
	*t = kbbText(number.String())
	return nil
}

// kbbSet says a field holds something: not absent, null, false or "".
func kbbSet(raw json.RawMessage) bool {
	switch strings.TrimSpace(string(raw)) {
	case "", "null", "false", `""`:
		return false
	}
	return true
}

type kbbPrice struct {
	Type  string      `json:"type"`
	Value json.Number `json:"value"`
	Low   json.Number `json:"low"`
	High  json.Number `json:"high"`
}

// kbbPrices is one carousel entry's values. Each level may be an object or a
// list of them, and every value found is kept.
type kbbPrices struct {
	Data kbbList[struct {
		APIData kbbList[struct {
			Vehicle kbbList[struct {
				Values []kbbPrice `json:"values"`
			}] `json:"vehicle"`
		}] `json:"APIData"`
	}] `json:"Data"`
}

func (p kbbPrices) find(kind string) (kbbPrice, bool) {
	for _, data := range p.Data {
		for _, api := range data.APIData {
			for _, vehicle := range api.Vehicle {
				for _, price := range vehicle.Values {
					if price.Type == kind {
						return price, true
					}
				}
			}
		}
	}
	return kbbPrice{}, false
}

type kbbList[T any] []T

func (l *kbbList[T]) UnmarshalJSON(raw []byte) error {
	trimmed := bytes.TrimSpace(raw)
	switch {
	case !kbbSet(trimmed):
		*l = nil
		return nil
	case trimmed[0] == '[':
		var many []T
		if err := json.Unmarshal(trimmed, &many); err != nil {
			return err
		}
		*l = many
		return nil
	default:
		var one T
		if err := json.Unmarshal(trimmed, &one); err != nil {
			return err
		}
		*l = kbbList[T]{one}
		return nil
	}
}
