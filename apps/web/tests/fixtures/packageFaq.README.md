The FAQ parity tests use public, read-only production responses for Rectangle,
iTerm2, Discord and Ely Office. `packageFaq.production.json` records the capture
time, source URLs and SHA-256 of each original response. It projects the responses
to the fields read by FAQ generation and package normalization; the retained fact
values are unchanged. Tests make no network requests.

`packageFaq.main.ts` freezes main's FAQ implementation at commit
`baa5a12ec65b0872703cf88563d1945bb40b6c2c`, blob
`e27df3cd0845c36204ac7726240fd2afe1e3f27d`. Only its relative import paths and
provenance comment differ from that source. It is an independent oracle, so keep
its answer logic unchanged when modifying the production implementation.

All four records are compared in all ten interface locales. Tests also overlay
future repository signals using each record's real repository URL and require
all existing question and answer strings to remain byte-identical. Only the new
public-repository Q&A may be added, within the existing eight-question limit.
