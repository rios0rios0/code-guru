package claude_test

import "strings"

// The helpers below answer about one variable of an environment at a time. An
// environment, whether a stub recorded it or the code under test built it for a
// process, can hold a credential of the shell the tests run in
// (ANTHROPIC_AUTH_TOKEN, a proxy URL with a password in it), and an assertion
// handed a whole environment prints all of it when it fails. Assert on what
// these return, never on the environment itself.

// variableValues returns the values env's NAME=value entries give the variable
// name, and nothing else.
func variableValues(env []string, name string) []string {
	var values []string
	for _, entry := range env {
		if entryName, value, found := strings.Cut(entry, "="); found && entryName == name {
			values = append(values, value)
		}
	}
	return values
}

// variableNames returns the names of env's NAME=value entries, never their
// values. It is for a list the code under test builds, one entry per line: in a
// recorded environment a value can span lines that are not entries, so look a
// variable up there by name (variableValues) instead.
func variableNames(env []string) []string {
	names := make([]string, 0, len(env))
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		names = append(names, name)
	}
	return names
}

// codeGuruVariableNames returns the names, never the values, of the
// CODE_GURU_* variables among env's NAME=value entries.
func codeGuruVariableNames(env []string) []string {
	var names []string
	for _, entry := range env {
		if name, _, _ := strings.Cut(entry, "="); strings.HasPrefix(name, "CODE_GURU_") {
			names = append(names, name)
		}
	}
	return names
}
