package config

import "testing"

func TestMapEnvGet(t *testing.T) {
	env := MapEnv{"TEST_VAR": "hello"}
	if got := env.Get("TEST_VAR", "fallback"); got != "hello" {
		t.Fatalf("Get existing = %q", got)
	}
	if got := env.Get("MISSING", "fallback"); got != "fallback" {
		t.Fatalf("Get default = %q", got)
	}
}

func TestMapEnvBool(t *testing.T) {
	for _, value := range []string{"1", "true", "yes", "on", "TRUE", "Yes"} {
		env := MapEnv{"TEST_BOOL": value}
		got, err := env.Bool("TEST_BOOL", false)
		if err != nil {
			t.Fatalf("Bool(%q) returned error: %v", value, err)
		}
		if !got {
			t.Fatalf("Bool(%q) = false", value)
		}
	}

	for _, value := range []string{"0", "false", "no", "off", "random"} {
		env := MapEnv{"TEST_BOOL": value}
		got, err := env.Bool("TEST_BOOL", true)
		if err != nil {
			t.Fatalf("Bool(%q) returned error: %v", value, err)
		}
		if got {
			t.Fatalf("Bool(%q) = true", value)
		}
	}

	got, err := (MapEnv{}).Bool("TEST_BOOL", true)
	if err != nil {
		t.Fatalf("Bool default returned error: %v", err)
	}
	if !got {
		t.Fatal("Bool default true returned false")
	}
}

func TestMapEnvInt(t *testing.T) {
	env := MapEnv{"MY_INT": "42"}
	got, err := env.Int("MY_INT", "10", 1, nil)
	if err != nil {
		t.Fatalf("Int returned error: %v", err)
	}
	if got != 42 {
		t.Fatalf("Int = %d", got)
	}

	got, err = (MapEnv{}).Int("MY_INT", "10", 1, nil)
	if err != nil {
		t.Fatalf("Int default returned error: %v", err)
	}
	if got != 10 {
		t.Fatalf("Int default = %d", got)
	}
}

func TestMapEnvIntErrors(t *testing.T) {
	if _, err := (MapEnv{"MY_INT": "abc"}).Int("MY_INT", "10", 1, nil); err == nil {
		t.Fatal("expected non-integer error")
	}
	if _, err := (MapEnv{"MY_INT": "0"}).Int("MY_INT", "10", 1, nil); err == nil {
		t.Fatal("expected below-minimum error")
	}
	max := 65535
	if _, err := (MapEnv{"MY_INT": "70000"}).Int("MY_INT", "10", 1, &max); err == nil {
		t.Fatal("expected above-maximum error")
	}
}
