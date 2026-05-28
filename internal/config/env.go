package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type LookupFunc func(string) (string, bool)

type MapEnv map[string]string

func OSEnv(key string) (string, bool) {
	return os.LookupEnv(key)
}

func OSMapEnv() MapEnv {
	env := MapEnv{}
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			env[key] = value
		}
	}
	return env
}

func (e MapEnv) Lookup(key string) (string, bool) {
	value, ok := e[key]
	return value, ok
}

func (e MapEnv) Get(key string, fallback string) string {
	if value, ok := e.Lookup(key); ok {
		return value
	}
	return fallback
}

func (e MapEnv) Bool(key string, fallback bool) (bool, error) {
	return BoolFrom(e.Lookup, key, fallback)
}

func (e MapEnv) Int(key string, fallback string, min int, max *int) (int, error) {
	return IntFrom(e.Lookup, key, fallback, min, max)
}

func BoolFrom(lookup LookupFunc, key string, fallback bool) (bool, error) {
	raw, ok := lookup(key)
	if !ok {
		return fallback, nil
	}
	return Truthy[strings.ToLower(raw)], nil
}

func IntFrom(lookup LookupFunc, key string, fallback string, min int, max *int) (int, error) {
	raw, ok := lookup(key)
	if !ok {
		raw = fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer (got %q)", key, raw)
	}
	if value < min {
		return 0, fmt.Errorf("%s must be >= %d (got %d)", key, min, value)
	}
	if max != nil && value > *max {
		return 0, fmt.Errorf("%s must be <= %d (got %d)", key, *max, value)
	}
	return value, nil
}
