## Novità

- La pulizia ora può rimuovere cache rigenerabili, registri e download abbandonati degli strumenti di programmazione con IA, tra cui Codex, Claude Code, Cursor, Antigravity, Continue e opencode. Sessioni, memorie e credenziali restano.
- In un progetto che usi ancora, i file di build incrementale si possono rimuovere da soli. Questo rallenta solo la build successiva. Ricostruire l’intero progetto resta una scelta separata e parte non selezionata.
- Gli archivi della cronologia IA restano di sola lettura. I file di trascrizione più vecchi di 30 giorni si possono spostare nel Cestino uno alla volta. La verifica mostra quante sessioni rientrano in 30, 90, 180 e 365 giorni, oltre alle date più vecchia e più recente.
- I pesi dei modelli scaricati per Ollama, LM Studio, Hugging Face, Jan e GPT4All si possono spostare nel Cestino. Chat, set di dati e indici di documenti locali restano. Se quello strumento è in esecuzione, il suo archivio resta al suo posto. MCP locale non può selezionare queste voci.
- I worktree Git e i cloni duplicati sono elencati con lo spazio che occupano. Un worktree collegato viene rimosso solo se Git lo accetta, quindi il lavoro non confermato viene rifiutato; il branch e il repository principale restano. Un clone duplicato va nel Cestino solo se è pulito, completamente inviato e senza stash. Un worktree danneggiato viene mostrato e non si può rimuovere da qui. Le righe oltre il limite dell’elenco sono contate, non mostrate.

## Miglioramenti

- Un progetto senza modifiche al codice sorgente da 30 giorni può offrire il risultato della build per la pulizia. Un progetto con modifiche recenti resta attivo, quindi viene offerta solo la build incrementale, più piccola.
- I file degli ultimi 7 giorni restano di sola lettura. Una destinazione da 7 a 30 giorni viene offerta non selezionata. Solo una destinazione che il controllo di attività ha trovato inutilizzata parte selezionata. Una cartella e i file al suo interno non vengono contati due volte.

## Correzioni

- Ogni cache di Cursor viene mostrata con la propria evidenza.
- La pulizia dei modelli locali elenca solo i file dei modelli, e solo se quella cartella esiste.
- Rimuovere un worktree controlla di nuovo che la cartella appartenga ancora a quel repository.
