package trusteekeys

import (
	"encoding/json"
	"log"
)

type trusteeResource struct {
	path string
	val  []byte
}

type trusteekeys struct {
	resources []trusteeResource
}

var TrusteeKeys trusteekeys

func (tk *trusteekeys) Update(data []byte) {
	err := json.Unmarshal(data, &tk.resources)
	if err != nil {
		log.Printf("Error in trusteekeys Update: %v\n", err)
	}
}
