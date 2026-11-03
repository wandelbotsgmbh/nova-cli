package simulation

// openapi.json is the streaming session broker's spec, fetched unchanged from
// https://broker.omni.dev.wandelbots.io/openapi.json; fixes go into overlay.yaml.
//go:generate go tool oapi-codegen -config oapi-codegen.yaml openapi.json
