package ui

import "embed"

// FS contains the compiled web panel assets.
//
//go:embed index.html assets/*
var FS embed.FS
