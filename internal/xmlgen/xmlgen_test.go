package xmlgen

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/n3wscott/phonebook/internal/model"
)

func TestBuildMatchesGolden(t *testing.T) {
	gid := 0
	contacts := []model.Contact{
		{
			FirstName: "John",
			LastName:  "Doe",
			Extension: "8000",
			GroupID:   &gid,
			Phones: []model.Phone{
				{Number: "8000", AccountIndex: 1},
				{Number: "8100", AccountIndex: 2},
			},
		},
		{
			FirstName: "Lily",
			LastName:  "Lee",
			Extension: "6000",
			Phones: []model.Phone{
				{Number: "6000", AccountIndex: 2},
			},
		},
	}

	got, err := Build(contacts)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	goldenPath := filepath.Join("..", "..", "testdata", "xml", "expected.xml")
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("failed to read golden file: %v", err)
	}

	if string(got) != string(want) {
		t.Fatalf("XML output mismatch\nGot:\n%s\nWant:\n%s", got, want)
	}
}
