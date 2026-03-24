package data

import _ "embed"

//go:embed ticker_map.json
var TickerMapJSON []byte

//go:embed holdings_template.csv
var HoldingsTemplateCSV []byte
