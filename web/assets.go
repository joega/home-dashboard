package web

import "embed"

//go:embed templates/*.html static/* static/vendor/*
var Files embed.FS
