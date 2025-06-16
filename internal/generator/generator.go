package generator

import (
	"EBS-PROJECT/config"
	pb "EBS-PROJECT/ebs-project/pkg/ebs"
	"fmt"
	"math/rand"
)

var otherOperators = []string{">", "<", ">=", "<="}

func GeneratePublication(cfg *config.Config, r *rand.Rand) *pb.Publication {
	vals := cfg.PublicationVals
	return &pb.Publication{
		StationId: fmt.Sprintf("%d", r.Intn(vals.StationIDRange.Max-vals.StationIDRange.Min+1)+vals.StationIDRange.Min),
		City:      vals.Cities[r.Intn(len(vals.Cities))],
		Temp:      int32(r.Intn(vals.TempRange.Max-vals.TempRange.Min+1) + vals.TempRange.Min),
		Rain:      r.Float64()*(vals.RainRange.Max-vals.RainRange.Min) + vals.RainRange.Min,
		Wind:      int32(r.Intn(vals.WindRange.Max-vals.WindRange.Min+1) + vals.WindRange.Min),
		Direction: vals.Directions[r.Intn(len(vals.Directions))],
		Date:      fmt.Sprintf("%d.%02d.%d", r.Intn(28)+1, r.Intn(12)+1, 2024),
	}
}

func GenerateSubscriptions(subscriberID string, count int, cfg *config.Config, r *rand.Rand) ([]*pb.SimpleSubscription, []*pb.ComplexSubscription) {
	var simpleSubs []*pb.SimpleSubscription
	var complexSubs []*pb.ComplexSubscription

	for i := 0; i < count; i++ {
		if r.Float64() < cfg.SubscriptionCfg.ComplexSubscriptionChance {
			complexSubs = append(complexSubs, generateComplexSubscription(subscriberID, cfg, r))
		} else {
			sub := generateSimpleSubscription(subscriberID, cfg, r)
			if len(sub.Constraints) > 0 {
				simpleSubs = append(simpleSubs, sub)
			}
		}
	}
	return simpleSubs, complexSubs
}

func generateSimpleSubscription(subscriberID string, cfg *config.Config, r *rand.Rand) *pb.SimpleSubscription {
	sub := &pb.SimpleSubscription{SubscriberId: subscriberID}
	subCfg := cfg.SubscriptionCfg

	for _, field := range subCfg.FieldFrequencies {
		if r.Float64() >= field.Frequency {
			continue
		}

		op := "="
		if field.Name == subCfg.EqualityOperatorFrequency.FieldName {
			if r.Float64() > subCfg.EqualityOperatorFrequency.Frequency {
				if isFieldNumeric(field.Name) {
					op = otherOperators[r.Intn(len(otherOperators))]
				} else {
					op = "!="
				}
			}
		}

		constraint := &pb.Constraint{
			Field:    field.Name,
			Operator: op,
			Value:    getRandomValueForField(field.Name, cfg.PublicationVals, r),
		}
		sub.Constraints = append(sub.Constraints, constraint)
	}
	return sub
}

func generateComplexSubscription(subscriberID string, cfg *config.Config, r *rand.Rand) *pb.ComplexSubscription {
	city := cfg.PublicationVals.Cities[r.Intn(len(cfg.PublicationVals.Cities))]
	aggField := cfg.SubscriptionCfg.AggregateFields[r.Intn(len(cfg.SubscriptionCfg.AggregateFields))]
	aggOperator := otherOperators[r.Intn(len(otherOperators))]
	var aggValue *pb.Value

	if aggField == "temp" {
		val := r.Intn(cfg.PublicationVals.TempRange.Max-cfg.PublicationVals.TempRange.Min) + cfg.PublicationVals.TempRange.Min
		aggValue = &pb.Value{ValueType: &pb.Value_IntValue{IntValue: int32(val)}}
	} else { // wind
		val := r.Intn(cfg.PublicationVals.WindRange.Max-cfg.PublicationVals.WindRange.Min) + cfg.PublicationVals.WindRange.Min
		aggValue = &pb.Value{ValueType: &pb.Value_IntValue{IntValue: int32(val)}}
	}

	return &pb.ComplexSubscription{
		SubscriberId: subscriberID,
		IdentityConstraint: &pb.Constraint{
			Field:    "city",
			Operator: "=",
			Value:    &pb.Value{ValueType: &pb.Value_StringValue{StringValue: city}},
		},
		AggregateConstraints: []*pb.Constraint{
			{
				Field:    "avg_" + aggField,
				Operator: aggOperator,
				Value:    aggValue,
			},
		},
	}
}

func isFieldNumeric(fieldName string) bool {
	return fieldName == "temp" || fieldName == "wind" || fieldName == "rain"
}

func getRandomValueForField(fieldName string, vals config.PublicationValues, r *rand.Rand) *pb.Value {
	switch fieldName {
	case "city":
		return &pb.Value{ValueType: &pb.Value_StringValue{StringValue: vals.Cities[r.Intn(len(vals.Cities))]}}
	case "temp":
		return &pb.Value{ValueType: &pb.Value_IntValue{IntValue: int32(r.Intn(vals.TempRange.Max-vals.TempRange.Min+1) + vals.TempRange.Min)}}
	case "wind":
		return &pb.Value{ValueType: &pb.Value_IntValue{IntValue: int32(r.Intn(vals.WindRange.Max-vals.WindRange.Min+1) + vals.WindRange.Min)}}
	case "direction":
		return &pb.Value{ValueType: &pb.Value_StringValue{StringValue: vals.Directions[r.Intn(len(vals.Directions))]}}
	default:
		return nil
	}
}
