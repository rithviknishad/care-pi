package s3admin

import (
	"encoding/json"
	"testing"
)

func TestFacilityPolicy(t *testing.T) {
	var doc struct {
		Statement []struct {
			Effect    string
			Principal string
			Action    []string
			Resource  []string
		}
	}
	if err := json.Unmarshal([]byte(FacilityPolicy("facility-bucket")), &doc); err != nil {
		t.Fatal(err)
	}
	s := doc.Statement[0]
	if s.Effect != "Allow" || s.Principal != "*" || s.Action[0] != "s3:GetObject" || s.Resource[0] != "arn:aws:s3:::facility-bucket/*" {
		t.Fatalf("policy: %+v", s)
	}
}
