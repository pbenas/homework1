package config

import "testing"

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(nil, func(string) string { return "" })
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Port != 8080 ||
		cfg.BindAddress != "127.0.0.1" ||
		cfg.Backend != BackendMemory ||
		cfg.DataDir != "./data" ||
		cfg.MaxObjectSize != DefaultMaxObjectSize {
		t.Fatalf("Load() = %+v", cfg)
	}
}

func TestLoadEnvironmentAndFlagPrecedence(t *testing.T) {
	environment := map[string]string{
		EnvPort:          "9000",
		EnvBindAddress:   "127.0.0.2",
		EnvBackend:       "disk",
		EnvDataDir:       "/environment-data",
		EnvMaxObjectSize: "2048",
	}
	getenv := func(key string) string { return environment[key] }

	cfg, err := Load([]string{
		"--port=9001",
		"--bind-address=::1",
		"--backend=memory",
		"--data-dir=/flag-data",
		"--max-object-size=1024",
	}, getenv)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Port != 9001 ||
		cfg.BindAddress != "::1" ||
		cfg.Backend != BackendMemory ||
		cfg.DataDir != "/flag-data" ||
		cfg.MaxObjectSize != 1024 {
		t.Fatalf("Load() = %+v", cfg)
	}
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "port too low", args: []string{"--port=0"}},
		{name: "port too high", args: []string{"--port=65536"}},
		{name: "unknown backend", args: []string{"--backend=remote"}},
		{name: "invalid bind address", args: []string{"--bind-address=localhost"}},
		{name: "zero object size", args: []string{"--max-object-size=0"}},
		{name: "empty disk directory", args: []string{"--backend=disk", "--data-dir="}},
		{name: "unknown flag", args: []string{"--unknown"}},
		{name: "positional argument", args: []string{"unexpected"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Load(test.args, func(string) string { return "" }); err == nil {
				t.Fatal("Load() error = nil")
			}
		})
	}
}

func TestLoadRejectsInvalidEnvironmentNumbers(t *testing.T) {
	for _, key := range []string{EnvPort, EnvMaxObjectSize} {
		t.Run(key, func(t *testing.T) {
			_, err := Load(nil, func(candidate string) string {
				if candidate == key {
					return "not-a-number"
				}
				return ""
			})
			if err == nil {
				t.Fatal("Load() error = nil")
			}
		})
	}
}
