package clientsvc

import (
	"errors"
	"testing"
)

func TestCancelRefusalMatchesSentinel(t *testing.T) {
	contract := map[string]interface{}{
		"commitments": []interface{}{map[string]interface{}{
			"id": "c1",
			"offer": map[string]interface{}{"offerAttributes": map[string]interface{}{
				"policies": map[string]interface{}{"cancellation": map[string]interface{}{"allowed": false}},
			}},
		}},
	}
	err := checkCancellationCutoff(contract, []string{"c1"})
	if !errors.Is(err, ErrCancelNotAllowed) {
		t.Fatalf("want ErrCancelNotAllowed, got %v", err)
	}
	if err.Error() != "this item's offer does not allow cancellation" {
		t.Fatalf("message changed: %q", err.Error())
	}
	if checkCancellationCutoff(contract, nil) != nil {
		t.Fatal("no chosen commitments must not refuse")
	}
}
