package api

import (
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
)

// The procedure wire's conversions, the counterpart of json.go's for REST.

func moneyProto(m domain.Money) *agentifiv1.Money {
	return &agentifiv1.Money{Amount: m.String()}
}

// nullableMoneyProto is unset when has is false: absent, not zero.
func nullableMoneyProto(m domain.Money, has bool) *agentifiv1.NullableMoney {
	if !has {
		return nil
	}
	return &agentifiv1.NullableMoney{Amount: m.String()}
}

// moneyFrom reads a Money or NullableMoney field, refusing text that is not an
// amount with the field it was sent in.
func moneyFrom(m interface{ GetAmount() string }, loc ...string) (domain.Money, error) {
	amount, err := domain.FromString(m.GetAmount())
	if err != nil {
		return domain.Zero, errInvalid("decimal_parsing", loc, "%s", domain.NotAnAmount(m.GetAmount()))
	}
	return amount, nil
}

// rateProto is an optional rate field: unset when has is false.
func rateProto(r domain.Rate, has bool) *string {
	if !has {
		return nil
	}
	return proto.String(r.String())
}

// idFrom reads an id field. A malformed id names no row this caller may see,
// so it is the same 404 as a row in another space.
func idFrom(raw, what string) (uuid.UUID, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, errNotFound(what)
	}
	return id, nil
}

// idProto is an optional id field: unset for the nil id, which is how
// internal/store spells none.
func idProto(id uuid.UUID) *string {
	if id == uuid.Nil {
		return nil
	}
	return proto.String(id.String())
}

func idPtrProto(id *uuid.UUID) *string {
	if id == nil {
		return nil
	}
	return proto.String(id.String())
}

func uuidStrings(ids []uuid.UUID) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, id.String())
	}
	return out
}

// dateProto is an optional date field: unset for the zero date.
func dateProto(d domain.Date) *string {
	if d.IsZero() {
		return nil
	}
	return proto.String(d.String())
}

func datePtrProto(d *Date) *string {
	if d == nil {
		return nil
	}
	return proto.String(domain.Date(*d).String())
}

func timestampProto(at *time.Time) *timestamppb.Timestamp {
	if at == nil {
		return nil
	}
	return timestamppb.New(*at)
}

// moneyPtrProto and ratePtrProto carry a nil through as unset.
func moneyPtrProto(m *domain.Money) *agentifiv1.NullableMoney {
	if m == nil {
		return nil
	}
	return nullableMoneyProto(*m, true)
}

func ratePtrProto(r *domain.Rate) *string {
	if r == nil {
		return nil
	}
	return proto.String(r.String())
}

// patchMask is the fields an Update request asks to change. Named in
// update_mask and unset is a field to clear; not named is a field to leave
// alone. Without update_mask, the fields set are the change and nothing is
// cleared. A set field the mask leaves out is refused rather than dropped. A
// repeated field is replaced whole: named and empty empties it, and without
// update_mask an empty one cannot be told from one not sent.
type patchMask map[string]bool

// patchable is a field a patch can name: one with presence, or a list.
func patchable(field protoreflect.FieldDescriptor) bool {
	return field.HasPresence() || field.IsList()
}

func maskOf(req proto.Message) (patchMask, error) {
	message := req.ProtoReflect()
	fields := message.Descriptor().Fields()
	maskField := fields.ByName("update_mask")
	out := patchMask{}

	if maskField == nil || !message.Has(maskField) {
		message.Range(func(field protoreflect.FieldDescriptor, _ protoreflect.Value) bool {
			if patchable(field) && field != maskField {
				out[string(field.Name())] = true
			}
			return true
		})
		return out, nil
	}

	mask, _ := message.Get(maskField).Message().Interface().(*fieldmaskpb.FieldMask)
	for _, path := range mask.GetPaths() {
		field := fields.ByName(protoreflect.Name(path))
		if field == nil || field == maskField || !patchable(field) {
			return nil, errInvalid("extra_forbidden", []string{"body", path},
				"%s is not a field this request can change", path)
		}
		out[path] = true
	}
	var unnamed error
	message.Range(func(field protoreflect.FieldDescriptor, _ protoreflect.Value) bool {
		name := string(field.Name())
		if patchable(field) && field != maskField && !out[name] {
			unnamed = errInvalid("extra_forbidden", []string{"body", name},
				"%s is set but update_mask does not name it", name)
			return false
		}
		return true
	})
	return out, unnamed
}

// optOf is one patch field as handler logic reads it (applyRequired,
// applyNullable): absent, cleared, or a value.
func optOf[T any](mask patchMask, name string, value *T) Opt[T] {
	if !mask[name] {
		return Opt[T]{}
	}
	if value == nil {
		return Opt[T]{Set: true, Null: true}
	}
	return Opt[T]{Set: true, Value: *value}
}

// optMoneyOf is optOf for an amount, which is parsed on the way.
func optMoneyOf(mask patchMask, name string, value *agentifiv1.NullableMoney) (Opt[domain.Money], error) {
	if !mask[name] {
		return Opt[domain.Money]{}, nil
	}
	if value == nil {
		return Opt[domain.Money]{Set: true, Null: true}, nil
	}
	amount, err := moneyFrom(value, "body", name)
	if err != nil {
		return Opt[domain.Money]{}, err
	}
	return Opt[domain.Money]{Set: true, Value: amount}, nil
}
