package config

import (
	"io"
	"strings"
	"testing"
	"time"
)

func envFrom(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestParseSettings(t *testing.T) {
	base := map[string]string{"PULSEBOARD_INGEST_TOKEN": "tok"}
	with := func(extra map[string]string) map[string]string {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}

	tests := []struct {
		name string
		env  map[string]string
		args []string
		want Config
	}{
		{"defaults", base, nil,
			Config{Addr: ":8080", DBPath: "./pulseboard.db", IngestToken: "tok", Retention: 168 * time.Hour}},
		{"addr env", with(map[string]string{"PULSEBOARD_ADDR": ":9090"}), nil,
			Config{Addr: ":9090", DBPath: DefaultDB, IngestToken: "tok", Retention: DefaultRetention}},
		{"addr flag over env", with(map[string]string{"PULSEBOARD_ADDR": ":9090"}), []string{"--addr", ":7070"},
			Config{Addr: ":7070", DBPath: DefaultDB, IngestToken: "tok", Retention: DefaultRetention}},
		{"db env", with(map[string]string{"PULSEBOARD_DB": "/tmp/a.db"}), nil,
			Config{Addr: DefaultAddr, DBPath: "/tmp/a.db", IngestToken: "tok", Retention: DefaultRetention}},
		{"db flag over env", with(map[string]string{"PULSEBOARD_DB": "/tmp/a.db"}), []string{"--db", "/tmp/b.db"},
			Config{Addr: DefaultAddr, DBPath: "/tmp/b.db", IngestToken: "tok", Retention: DefaultRetention}},
		{"token flag without env", map[string]string{}, []string{"--ingest-token", "flagtok"},
			Config{Addr: DefaultAddr, DBPath: DefaultDB, IngestToken: "flagtok", Retention: DefaultRetention}},
		{"token flag over env", base, []string{"--ingest-token", "flagtok"},
			Config{Addr: DefaultAddr, DBPath: DefaultDB, IngestToken: "flagtok", Retention: DefaultRetention}},
		{"retention env", with(map[string]string{"PULSEBOARD_RETENTION": "24h"}), nil,
			Config{Addr: DefaultAddr, DBPath: DefaultDB, IngestToken: "tok", Retention: 24 * time.Hour}},
		{"retention flag over env", with(map[string]string{"PULSEBOARD_RETENTION": "24h"}), []string{"--retention", "2h"},
			Config{Addr: DefaultAddr, DBPath: DefaultDB, IngestToken: "tok", Retention: 2 * time.Hour}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.args, envFrom(tt.env), io.Discard)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParseValidation(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		args    []string
		wantErr string
	}{
		{"missing token", map[string]string{}, nil, "ingest token"},
		{"negative retention flag", map[string]string{"PULSEBOARD_INGEST_TOKEN": "tok"}, []string{"--retention", "-1h"}, "retention"},
		{"zero retention env", map[string]string{"PULSEBOARD_INGEST_TOKEN": "tok", "PULSEBOARD_RETENTION": "0s"}, nil, "retention"},
		{"unparseable retention env", map[string]string{"PULSEBOARD_INGEST_TOKEN": "tok", "PULSEBOARD_RETENTION": "soon"}, nil, "retention"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.args, envFrom(tt.env), io.Discard)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error %q does not mention %q", err, tt.wantErr)
			}
		})
	}
}
