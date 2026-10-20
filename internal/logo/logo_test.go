package logo

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestICOHoldsOnePNGPerSize(t *testing.T) {
	ico := ICO(Amber, AmberDark, true)
	var head struct{ Reserved, Type, Count uint16 }
	if err := binary.Read(bytes.NewReader(ico), binary.LittleEndian, &head); err != nil {
		t.Fatal(err)
	}
	if head.Reserved != 0 || head.Type != 1 || head.Count != 5 {
		t.Fatalf("cabecera de ICO inesperada: %+v", head)
	}
	if n := bytes.Count(ico, []byte("\x89PNG")); n != 5 {
		t.Fatalf("el ICO debe llevar 5 PNG, lleva %d", n)
	}
}
