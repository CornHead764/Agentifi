package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// An invented VIN and an invented car; nothing here is a real vehicle.
const inventedVIN = "1EXAMPLE0VIN00001"

type kbbCall struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

// kbbSite answers the four queries by the alias each one uses, from answers;
// an alias missing from answers gets data with that alias null.
func kbbSite(t *testing.T, answers map[string]string) (*fakeSite, *[]kbbCall) {
	t.Helper()
	var calls []kbbCall
	mux := http.NewServeMux()
	mux.HandleFunc("/api/owners-argo/", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))
		var call kbbCall
		require.NoError(t, json.NewDecoder(r.Body).Decode(&call))
		calls = append(calls, call)
		w.Header().Set("Content-Type", "application/json")
		for _, alias := range []string{"v", "ymm", "d", "p"} {
			if strings.Contains(call.Query, "{ "+alias+": ") {
				answer, ok := answers[alias]
				if !ok {
					answer = "null"
				}
				if strings.HasPrefix(answer, "!") {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				fmt.Fprintf(w, `{"data":{%q:%s}}`, alias, answer)
				return
			}
		}
		t.Fatalf("an unexpected query: %s", call.Query)
	})
	return &fakeSite{handler: mux, cookie: "visitor=abc; ZipCode=00001; other=1"}, &calls
}

func kbbAnswers() map[string]string {
	return map[string]string{
		"v":   `{"url":"/example/","error":null,"make":"Examplemotors","model":"Roadster","year":"2011"}`,
		"ymm": `{"defaultVehicleId":"500001","typicalMileage":"98000","bodyStyles":[{"name":"Coupe","trims":[{"name":"Base","vehicleId":"500001"}]}]}`,
		"d":   `{"selectedVehicle":{"vehicleId":500002,"vehicleOptionIds":[7001,7002]},"trims":[{"vehicleId":500001,"trimName":"Base"},{"vehicleId":500002,"trimName":"Sport"}]}`,
		"p": `{"privateParty":{"Data":{"APIData":{"vehicle":{"values":[
				{"type":"PP_Excellent","value":6100,"low":5500,"high":6700},
				{"type":"PP_Good","value":5210,"low":4600,"high":5800},
				{"type":"PP_Fair","value":4400,"low":3800,"high":5000}]}}}},
			"tradeIn":{"Data":[{"APIData":[{"vehicle":[{"values":[{"type":"TradeIn","value":3050,"low":2500,"high":3600}]}]}]}]}}`,
	}
}

func TestTheGoodConditionPrivatePartyValueIsRecorded(t *testing.T) {
	site, calls := kbbSite(t, kbbAnswers())
	kbb := &KelleyBlueBook{Browser: site.open}

	quote, err := kbb.Estimate(context.Background(), ValuationSubject{
		AssetType: AssetTypeVehicle, VIN: strings.ToLower(inventedVIN), Mileage: 61000,
	})
	require.NoError(t, err)
	require.NotNil(t, quote)
	require.Equal(t, "5210.00", quote.Value.String())
	require.Equal(t, "USD", quote.Currency)
	priced := quote.Priced
	require.Equal(t, []string{"2011", "Examplemotors", "Roadster", "Sport"},
		[]string{priced.Year, priced.Make, priced.Model, priced.Trim})
	require.Equal(t, 61000, priced.Mileage)
	require.True(t, priced.HasMileage)
	require.False(t, priced.TypicalMileage)
	require.True(t, priced.HasRange)
	require.Equal(t, "4600.00", priced.Low.String())
	require.Equal(t, "5800.00", priced.High.String())
	require.Equal(t, json.Number("3050"), quote.Detail["trade_in"])
	require.Equal(t, "500002", quote.Detail["kbb_vehicle_id"])
	require.Equal(t, []string{"https://www.kbb.com/whats-my-car-worth/"}, site.origins)
	require.Equal(t, 1, site.closed, "the lookup's browser context is closed")

	require.Len(t, *calls, 4)
	require.Equal(t, map[string]any{"vin": inventedVIN}, (*calls)[0].Variables)
	require.Equal(t, map[string]any{
		"year": "2011", "make": "examplemotors", "model": "roadster", "vin": strings.ToLower(inventedVIN),
	}, (*calls)[1].Variables)
	require.Equal(t, map[string]any{"vin": strings.ToLower(inventedVIN), "id": float64(500001)}, (*calls)[2].Variables)
	require.Equal(t, map[string]any{
		"zip": "00001", "id": "500002", "opts": "7001|true|7002|true", "cond": "good", "miles": "61000",
	}, (*calls)[3].Variables)
}

func TestAnUnknownOdometerIsPricedAtKBBsTypicalMileage(t *testing.T) {
	site, calls := kbbSite(t, kbbAnswers())

	quote, err := (&KelleyBlueBook{Browser: site.open}).Estimate(context.Background(),
		ValuationSubject{VIN: inventedVIN, Mileage: -1})
	require.NoError(t, err)
	require.NotNil(t, quote)
	require.Equal(t, "98000", (*calls)[3].Variables["miles"])
	require.Equal(t, 98000, quote.Priced.Mileage)
	require.True(t, quote.Priced.HasMileage)
	require.True(t, quote.Priced.TypicalMileage)
}

func TestAZeroOdometerIsARealReading(t *testing.T) {
	site, calls := kbbSite(t, kbbAnswers())

	_, err := (&KelleyBlueBook{Browser: site.open}).Estimate(context.Background(),
		ValuationSubject{VIN: inventedVIN, Mileage: 0})
	require.NoError(t, err)
	require.Equal(t, "0", (*calls)[3].Variables["miles"])
}

func TestAMissingPieceIsNoEstimate(t *testing.T) {
	for name, change := range map[string]func(map[string]string){
		"the VIN decode reports an error": func(a map[string]string) {
			a["v"] = `{"url":null,"error":"VIN not found","make":null,"model":null,"year":null}`
		},
		"the VIN decodes to nothing": func(a map[string]string) { delete(a, "v") },
		"no default vehicle":         func(a map[string]string) { a["ymm"] = `{"defaultVehicleId":null,"typicalMileage":"98000"}` },
		"no selected vehicle":        func(a map[string]string) { a["d"] = `{"selectedVehicle":null,"trims":[]}` },
		"nothing priced":             func(a map[string]string) { delete(a, "p") },
		"no good-condition value": func(a map[string]string) {
			a["p"] = `{"privateParty":{"Data":{"APIData":{"vehicle":{"values":[{"type":"PP_Fair","value":4400}]}}}}}`
		},
		"a zero good-condition value": func(a map[string]string) {
			a["p"] = `{"privateParty":{"Data":{"APIData":{"vehicle":{"values":[{"type":"PP_Good","value":0}]}}}}}`
		},
	} {
		t.Run(name, func(t *testing.T) {
			answers := kbbAnswers()
			change(answers)
			site, _ := kbbSite(t, answers)
			quote, err := (&KelleyBlueBook{Browser: site.open}).Estimate(context.Background(),
				ValuationSubject{VIN: inventedVIN, Mileage: 61000})
			require.NoError(t, err)
			require.Nil(t, quote)
			require.Equal(t, 1, site.closed)
		})
	}
}

func TestNoZipCodeIsNoEstimate(t *testing.T) {
	site, calls := kbbSite(t, kbbAnswers())
	site.cookie = "visitor=abc"

	quote, err := (&KelleyBlueBook{Browser: site.open}).Estimate(context.Background(),
		ValuationSubject{VIN: inventedVIN, Mileage: 61000})
	require.NoError(t, err)
	require.Nil(t, quote)
	require.Empty(t, *calls, "nothing is asked of KBB without a ZIP code")
	require.Equal(t, 1, site.closed, "a lookup that stops early still closes its context")
}

func TestARefusedQueryIsRateLimited(t *testing.T) {
	answers := kbbAnswers()
	answers["p"] = "!"
	site, _ := kbbSite(t, answers)

	quote, err := (&KelleyBlueBook{Browser: site.open}).Estimate(context.Background(),
		ValuationSubject{VIN: inventedVIN, Mileage: 61000})
	require.ErrorIs(t, err, ErrValuationRateLimited)
	require.Nil(t, quote)
}

func TestAQueryKBBRejectsIsAnError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/owners-argo/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"errors":[{"message":"Cannot query field \"vehicleUrlByVinCads\""}],"data":null}`)
	})
	site := &fakeSite{handler: mux, cookie: "ZipCode=00001"}

	quote, err := (&KelleyBlueBook{Browser: site.open}).Estimate(context.Background(),
		ValuationSubject{VIN: inventedVIN, Mileage: 61000})
	require.ErrorContains(t, err, "Cannot query field")
	require.Nil(t, quote)
}

func TestNoVINOpensNoBrowser(t *testing.T) {
	site, _ := kbbSite(t, kbbAnswers())
	quote, err := (&KelleyBlueBook{Browser: site.open}).Estimate(context.Background(),
		ValuationSubject{VIN: "  ", Mileage: 61000})
	require.NoError(t, err)
	require.Nil(t, quote)
	require.Zero(t, site.opened)
}

func TestTheZipCodeIsReadFromItsOwnCookie(t *testing.T) {
	for cookie, want := range map[string]string{
		"ZipCode=00001":                     "00001",
		"a=1; ZipCode=00002; b=2":           "00002",
		"LastZipCode=00003":                 "",
		"ZipCode=":                          "",
		"a=1; LastZipCode=9; ZipCode=00004": "00004",
	} {
		match := kbbZipCookie.FindStringSubmatch(cookie)
		got := ""
		if match != nil {
			got = match[1]
		}
		require.Equal(t, want, got, cookie)
	}
}
