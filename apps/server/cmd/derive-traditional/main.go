// JSON stdin/stdout bridge for offline repository generation. No credentials or network.
package main

import (
	"encoding/json"
	"os"

	"github.com/opennavo/opennavo/server/internal/traditional"
)

func main() {
	var fields map[string]any
	if err := json.NewDecoder(os.Stdin).Decode(&fields); err != nil {
		os.Exit(1)
	}
	result, err := traditional.Fields(fields, nil)
	if err != nil {
		os.Exit(1)
	}
	if err = json.NewEncoder(os.Stdout).Encode(result); err != nil {
		os.Exit(1)
	}
}
