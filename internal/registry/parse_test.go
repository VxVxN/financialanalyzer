package registry

import (
	"reflect"
	"testing"
)

func TestParseTickers(t *testing.T) {
	if ParseTickers("  ") != nil {
		t.Fatal("empty should be nil")
	}
	got := ParseTickers("nlmk, PHOR\n ozon")
	want := map[string]struct{}{"NLMK": {}, "PHOR": {}, "OZON": {}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}
