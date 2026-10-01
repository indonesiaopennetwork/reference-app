package clientsvc

import (
	"errors"
	"testing"
)

func TestCheckQuoteAvailable(t *testing.T) {
	priced := []interface{}{map[string]interface{}{"id": "c1"}}
	closed := map[string]interface{}{
		"status":    map[string]interface{}{"descriptor": map[string]interface{}{"code": "CLOSED"}},
		"resources": []interface{}{map[string]interface{}{"id": "item-gone"}},
	}
	open := map[string]interface{}{"resources": []interface{}{map[string]interface{}{"id": "item-ok"}}}

	cases := []struct {
		name     string
		contract map[string]interface{}
		wantErr  bool
	}{
		{"all available and priced", map[string]interface{}{"commitments": []interface{}{open}, "consideration": priced}, false},
		{"an item the seller no longer has", map[string]interface{}{"commitments": []interface{}{open, closed}, "consideration": priced}, true},
		{"nothing priced", map[string]interface{}{"commitments": []interface{}{open}}, true},
	}
	for _, tc := range cases {
		err := checkQuoteAvailable(tc.contract)
		if tc.wantErr != (err != nil) {
			t.Errorf("%s: err = %v", tc.name, err)
		}
		if err != nil && !errors.Is(err, ErrQuoteUnavailable) {
			t.Errorf("%s: %v is not ErrQuoteUnavailable", tc.name, err)
		}
	}
}
