package api

import (
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

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

// patchMask is the fields an Update request asks to change. Named in
// update_mask and unset is a field to clear; not named is a field to leave
// alone. Without update_mask, the fields set are the change and nothing is
// cleared. A set field the mask leaves out is refused rather than dropped. A
// repeated field is replaced whole when named, so named and empty empties it.
type patchMask map[string]bool

// patchable is a field update_mask may name: one with presence, or a list.
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
