package sqlite

import "testing"

func TestCanonicalLabels(t *testing.T) {
	tests := []struct {
		name   string
		labels map[string]string
		want   string
	}{
		{"nil", nil, `{}`},
		{"empty", map[string]string{}, `{}`},
		{"single", map[string]string{"host": "pi-1"}, `{"host":"pi-1"}`},
		{"sorted keys", map[string]string{"b": "2", "a": "1", "c": "3"}, `{"a":"1","b":"2","c":"3"}`},
		{"unicode values", map[string]string{"city": "Zürich", "emoji": "🚀"}, `{"city":"Zürich","emoji":"🚀"}`},
		{"no html escaping", map[string]string{"q": "a<b&c>d"}, `{"q":"a<b&c>d"}`},
		{"quotes escaped", map[string]string{"q": `say "hi"`}, `{"q":"say \"hi\""}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := canonicalLabels(tt.labels)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("got %s, want %s", got, tt.want)
			}
		})
	}
}

func TestCanonicalLabelsOrderIndependent(t *testing.T) {
	// Build the same label set with different insertion orders.
	a := map[string]string{}
	a["a"] = "1"
	a["b"] = "2"
	b := map[string]string{}
	b["b"] = "2"
	b["a"] = "1"
	ca, _ := canonicalLabels(a)
	cb, _ := canonicalLabels(b)
	if ca != cb {
		t.Errorf("%s != %s", ca, cb)
	}
}
