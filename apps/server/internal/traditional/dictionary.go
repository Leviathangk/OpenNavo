package traditional

import (
	"bufio"
	"embed"
	"fmt"
	"strings"
)

// The upstream text dictionaries and license are pinned with SHA-256 in
// data/upstream.json. No upstream executable code or native runtime is required.
//
//go:embed data/*.txt
var dictionaries embed.FS

type trie struct {
	children map[rune]*trie
	value    string
}
type chain []*trie

func loadDictionaries() (chain, error) {
	result := chain{}
	for _, files := range [][]string{{"STCharacters", "STPhrases"}, {"TWPhrasesIT", "TWPhrasesName", "TWPhrasesOther"}, {"TWVariants"}} {
		root := &trie{}
		for _, name := range files {
			raw, err := dictionaries.ReadFile("data/" + name + ".txt")
			if err != nil {
				return nil, err
			}
			scanner := bufio.NewScanner(strings.NewReader(string(raw)))
			for scanner.Scan() {
				line := scanner.Text()
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				key, values, ok := strings.Cut(line, "\t")
				if !ok || key == "" || len(strings.Fields(values)) == 0 {
					return nil, fmt.Errorf("invalid dictionary %s", name)
				}
				node := root
				for _, r := range key {
					if node.children == nil {
						node.children = map[rune]*trie{}
					}
					if node.children[r] == nil {
						node.children[r] = &trie{}
					}
					node = node.children[r]
				}
				node.value = strings.Fields(values)[0]
			}
			if err = scanner.Err(); err != nil {
				return nil, err
			}
		}
		result = append(result, root)
	}
	return result, nil
}

// Convert applies longest phrase matches before single-character fallbacks,
// then Taiwan terminology and Taiwan character variants, in the s2twp order.
func (c chain) Convert(input string) (string, error) {
	for _, root := range c {
		runes := []rune(input)
		var out strings.Builder
		for i := 0; i < len(runes); {
			node := root
			end := i
			replacement := ""
			for j := i; j < len(runes); j++ {
				node = node.children[runes[j]]
				if node == nil {
					break
				}
				if node.value != "" {
					replacement = node.value
					end = j + 1
				}
			}
			if end == i {
				out.WriteRune(runes[i])
				i++
			} else {
				out.WriteString(replacement)
				i = end
			}
		}
		input = out.String()
	}
	return input, nil
}
