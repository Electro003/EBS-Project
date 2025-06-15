package config

import (
	"encoding/json"
	"os"
)

type RangeInt struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

type RangeFloat struct {
	Min float64 `json:"min"`
	Max float64 `json:"max"`
}

type PublicationValues struct {
	StationIDRange RangeInt   `json:"stationIdRange"`
	Cities         []string   `json:"cities"`
	TempRange      RangeInt   `json:"tempRange"`
	RainRange      RangeFloat `json:"rainRange"`
	WindRange      RangeInt   `json:"windRange"`
	Directions     []string   `json:"directions"`
}

type FieldFrequency struct {
	Name      string  `json:"name"`
	Frequency float64 `json:"frequency"`
}

type EqualityOperatorFrequency struct {
	FieldName string  `json:"fieldName"`
	Frequency float64 `json:"frequency"`
}

type SubscriptionConfig struct {
	FieldFrequencies          []FieldFrequency          `json:"fieldFrequencies"`
	EqualityOperatorFrequency EqualityOperatorFrequency `json:"equalityOperatorFrequency"`
	ComplexSubscriptionChance float64                   `json:"complexSubscriptionChance"`
	AggregateFields           []string                  `json:"aggregateFields"`
}

type Config struct {
	BrokerAddresses              []string           `json:"brokerAddresses"`
	RoutingKeyField              string             `json:"routingKeyField"` // <-- CÂMPUL ADĂUGAT
	WindowSize                   int                `json:"windowSize"`
	PublicationIntervalMs        int                `json:"publicationIntervalMs"`
	EvaluationDurationSeconds    int                `json:"evaluationDurationSeconds"`
	TotalSubscriptionsToGenerate int                `json:"totalSubscriptionsToGenerate"`
	PublicationVals              PublicationValues  `json:"publicationValues"`
	SubscriptionCfg              SubscriptionConfig `json:"subscriptionConfig"`
}

func LoadConfig(path string) (*Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	config := &Config{}
	err = decoder.Decode(config)
	if err != nil {
		return nil, err
	}
	return config, nil
}
