package sitetpl

import _ "embed"

//go:embed default_templates/rutracker.org.yaml
var defaultRutrackerYAML []byte

//go:embed default_templates/nnmclub.to.yaml
var defaultNNMClubYAML []byte

//go:embed default_templates/tapochek.net.yaml
var defaultTapochekYAML []byte

func DefaultRutrackerTemplate() Template {
	tmpl, err := LoadTemplateBytes("rutracker.org.yaml", defaultRutrackerYAML)
	if err == nil {
		tmpl.Source = "built-in"
		return tmpl
	}
	return Template{Version: 1, Site: "rutracker.org", ID: "rutracker.org", Name: "RuTracker", Domains: []string{"rutracker.org"}, Kind: "forum_topic", Mode: ModeHTTP, Source: "built-in"}
}

func DefaultNNMClubTemplate() Template {
	tmpl, err := LoadTemplateBytes("nnmclub.to.yaml", defaultNNMClubYAML)
	if err == nil {
		tmpl.Source = "built-in"
		return tmpl
	}
	return Template{Version: 1, Site: "nnmclub.to", ID: "nnmclub.to", Name: "NNM-Club", Domains: []string{"nnmclub.to"}, Kind: "forum_topic", Mode: ModeHTTP, Source: "built-in"}
}

func DefaultTapochekTemplate() Template {
	tmpl, err := LoadTemplateBytes("tapochek.net.yaml", defaultTapochekYAML)
	if err == nil {
		tmpl.Source = "built-in"
		return tmpl
	}
	return Template{Version: 1, Site: "tapochek.net", ID: "tapochek.net", Name: "Tapochek.net", Domains: []string{"tapochek.net"}, Kind: "forum_topic", Mode: ModeHTTP, Source: "built-in"}
}
