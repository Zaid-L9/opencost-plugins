package newrelicplugin

type NewRelicConfig struct {
	APIKey                  string  `json:"new_relic_api_key"`
	AccountID               int     `json:"account_id"`
	Region                  string  `json:"region"`
	LogLevel                string  `json:"log_level"`
	DataIngestPriceGB       float64 `json:"data_ingest_price_per_gb"`
	CoreCCUPrice            float64 `json:"core_ccu_price"`
	AdvancedCCUPrice        float64 `json:"advanced_ccu_price"`
	SyntheticsPricePerCheck float64 `json:"synthetics_price_per_check"`
	NerdGraphAPIURL         string  `json:"nerdgraph_api_url"`
}
