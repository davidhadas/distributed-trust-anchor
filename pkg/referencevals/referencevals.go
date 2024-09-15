package referencevals

import (
	"encoding/json"
	"log"
)

type referenceVal struct {
	refVersion string
	refType    string
	refPayload string
}

type referencevals struct {
	resources []referenceVal
}

var ReferenceVals referencevals

func (rv *referencevals) Update(data []byte) {
	err := json.Unmarshal(data, &rv.resources)
	if err != nil {
		log.Printf("Error in referencevals Update: %v\n", err)
	}
}
