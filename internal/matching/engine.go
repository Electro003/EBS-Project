package matching

import (
	pb "EBS-PROJECT/proto"
	"log"
	"strings"
)

func CheckSimpleMatch(pub *pb.Publication, sub *pb.SimpleSubscription) bool {
	for _, constraint := range sub.Constraints {
		if !checkConstraint(pub, constraint) {
			return false
		}
	}
	return true
}

func CheckWindowMatch(window []*pb.Publication, sub *pb.ComplexSubscription) (bool, string) {
	if len(window) == 0 {
		return false, ""
	}

	for _, aggConstraint := range sub.AggregateConstraints {
		parts := strings.Split(aggConstraint.Field, "_")
		if len(parts) != 2 {
			continue
		}
		aggType, fieldName := parts[0], parts[1]

		var calculatedValue float64
		var sum float64
		for _, pub := range window {
			pubValue := getFieldValue(pub, fieldName)
			if val, ok := pubValue.(int32); ok {
				sum += float64(val)
			} else if val, ok := pubValue.(float64); ok {
				sum += val
			}
		}

		if aggType == "avg" {
			calculatedValue = sum / float64(len(window))
		}

		if !compare(calculatedValue, aggConstraint.Operator, aggConstraint.Value) {
			return false, ""
		}
	}

	msg := "Conditii de agregare indeplinite"
	return true, msg
}

func checkConstraint(pub *pb.Publication, c *pb.Constraint) bool {
	pubValue := getFieldValue(pub, c.Field)
	if pubValue == nil {
		return false // Campul nu exista in publicatie
	}
	return compare(pubValue, c.Operator, c.Value)
}

func getFieldValue(pub *pb.Publication, fieldName string) interface{} {
	switch strings.ToLower(fieldName) {
	case "station_id", "stationid":
		return pub.StationId
	case "city":
		return pub.City
	case "temp":
		return pub.Temp
	case "rain":
		return pub.Rain
	case "wind":
		return pub.Wind
	case "direction":
		return pub.Direction
	case "date":
		return pub.Date
	default:
		return nil
	}
}

func compare(pubValue interface{}, op string, subValue *pb.Value) bool {
	switch v := pubValue.(type) {
	case int32:
		subV := subValue.GetIntValue()
		switch op {
		case "=":
			return v == subV
		case "!=":
			return v != subV
		case ">":
			return v > subV
		case "<":
			return v < subV
		case ">=":
			return v >= subV
		case "<=":
			return v <= subV
		}
	case string:
		if subV, ok := subValue.GetValueType().(*pb.Value_StringValue); ok {
			switch op {
			case "=":
				return v == subV.StringValue
			case "!=":
				return v != subV.StringValue
			}
		}

	case float64:
		subV := subValue.GetDoubleValue()
		switch op {
		case "=":
			return v == subV
		case "!=":
			return v != subV
		case ">":
			return v > subV
		case "<":
			return v < subV
		case ">=":
			return v >= subV
		case "<=":
			return v <= subV
		}
	}
	if pv, ok := pubValue.(float64); ok {
		switch sv := subValue.GetValueType().(type) {
		case *pb.Value_IntValue:
			return compareFloatWithInt(pv, op, sv.IntValue)
		case *pb.Value_DoubleValue:
			return compareFloatWithFloat(pv, op, sv.DoubleValue)
		}
	}
	log.Printf("Tip de data neimplementat pentru comparare: %T", pubValue)
	return false
}

func compareFloatWithInt(pv float64, op string, sv int32) bool {
	svf := float64(sv)
	return compareFloatWithFloat(pv, op, svf)
}

func compareFloatWithFloat(pv float64, op string, sv float64) bool {
	switch op {
	case "=":
		return pv == sv
	case "!=":
		return pv != sv
	case ">":
		return pv > sv
	case "<":
		return pv < sv
	case ">=":
		return pv >= sv
	case "<=":
		return pv <= sv
	}
	return false
}
