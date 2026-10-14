package main

import _ "embed"

// Go sources embedded for TestReferencedKeysExist.
var (
	//go:embed handlers.go
	handlersSrc string
	//go:embed handlers_tally.go
	handlersTallySrc string
	//go:embed handlers_match.go
	handlersMatchSrc string
	//go:embed handlers_poster.go
	handlersPosterSrc string
	//go:embed handlers_player.go
	handlersPlayerSrc string
	//go:embed slug.go
	slugSrc string
	//go:embed dates.go
	datesSrc string
	//go:embed auth.go
	authSrc string
	//go:embed scoring.go
	scoringSrc string
	//go:embed aioauth.go
	aioauthSrc string
)
