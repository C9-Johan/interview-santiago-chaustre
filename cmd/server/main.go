package main

import (
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/sashabaranov/go-openai"
	"github.com/xeipuuv/gojsonschema"
)

// Placeholder imports — forces go.mod to retain the declared runtime
// dependencies until real code lands. Removed in subsequent tasks once
// each package has a real consumer.
var (
	_ = chi.NewRouter
	_ = openai.NewClient
	_ = gojsonschema.NewStringLoader
	_ = uuid.NewString
)

func main() {}
