package strictjson

import "testing"

func TestStrictFlatJSON(t *testing.T) {
	for _, raw := range []string{`null`, `[]`, `{"title":null}`, `{"Title":"x"}`, `{"title":"x","title":"y"}`, `{"title":"x","\u0074itle":"y"}`, `{"title":"\ud800"}`, `{"title":"\udc00"}`, `{"title":"\ud800\u0061"}`, `{"title":"\u0000"}`, `{"title":{}}`, `{} {}`, "{\"title\":\"\xff\"}"} {
		var target struct {
			Title *string `json:"title"`
		}
		if Decode([]byte(raw), 1000, &target, "title") == nil {
			t.Errorf("应拒绝 %q", raw)
		}
	}
	for _, raw := range []string{`{}`, `{"title":"\ud83d\ude00"}`, `{"title":"字\\u0000"}`, `{"title":"字"}`} {
		var target struct {
			Title *string `json:"title"`
		}
		if err := Decode([]byte(raw), 1000, &target, "title"); err != nil {
			t.Errorf("应接受 %q: %v", raw, err)
		}
	}
}
