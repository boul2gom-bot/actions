package main

// LabelType describes a GitHub label with its display color and description.
type LabelType struct {
	color       string
	description string
}

// predefined is the minimal set of universal labels managed by this action.
// Colors are hex strings without the leading '#'.
// Domain-specific labels should be passed via the label_definitions input.
var predefined = map[string]LabelType{
	"bug":             {"d73a4a", "Something isn't working"},
	"enhancement":     {"a2eeef", "New feature or request"},
	"refactor":        {"e4e669", "Code refactoring"},
	"documentation":   {"0075ca", "Improvements or additions to documentation"},
	"performance":     {"5319e7", "Performance improvement"},
	"security":        {"b60205", "Security fix or improvement"},
	"dependencies":    {"0366d6", "Pull requests that update a dependency file"},
	"ci":              {"e6e6e6", "Changes to CI configuration files and scripts"},
	"breaking-change": {"b60205", "Introduces a breaking API change"},
}
