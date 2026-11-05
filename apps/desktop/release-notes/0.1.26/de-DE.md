## Neu

- Die Bereinigung kann jetzt neu erzeugbare Caches, Protokolle und abgebrochene Downloads von KI-Programmierwerkzeugen entfernen, darunter Codex, Claude Code, Cursor, Antigravity, Continue und opencode. Sitzungen, Erinnerungen und Zugangsdaten bleiben.
- In einem Projekt, das Sie noch verwenden, lassen sich inkrementelle Build-Dateien einzeln entfernen. Das verlangsamt nur den nächsten Build. Das gesamte Projekt neu zu bauen bleibt eine eigene Auswahl und startet nicht ausgewählt.
- KI-Verlaufsspeicher bleiben schreibgeschützt. Transkriptdateien, die älter als 30 Tage sind, können einzeln in den Papierkorb verschoben werden. Die Prüfung zeigt, wie viele Sitzungen in 30, 90, 180 und 365 Tage fallen, sowie das älteste und das neueste Datum.
- Heruntergeladene Modellgewichte von Ollama, LM Studio, Hugging Face, Jan und GPT4All können in den Papierkorb verschoben werden. Chats, Datensätze und lokale Dokumentindizes bleiben. Läuft das Werkzeug, bleibt sein Speicher an Ort und Stelle. Lokales MCP kann diese Einträge nicht auswählen.
- Git-Worktrees und doppelte Klone werden mit dem belegten Speicherplatz aufgeführt. Ein verknüpfter Worktree wird nur entfernt, wenn Git zustimmt, ungesicherte Änderungen werden also abgelehnt; Branch und Hauptrepository bleiben. Ein doppelter Klon wandert nur dann in den Papierkorb, wenn er sauber und vollständig gepusht ist und kein Stash hat. Ein beschädigter Worktree wird angezeigt und kann hier nicht entfernt werden. Zeilen über dem Listenlimit werden gezählt, nicht angezeigt.

## Verbesserungen

- Ein Projekt ohne Quelländerungen seit 30 Tagen kann seine Build-Ausgabe zur Bereinigung anbieten. Ein Projekt mit neueren Änderungen gilt als aktiv, daher wird nur der kleinere inkrementelle Build angeboten.
- Dateien der letzten 7 Tage bleiben schreibgeschützt. Ein Ziel von 7 bis 30 Tagen wird unausgewählt angeboten. Nur ein Ziel, das die Aktivitätsprüfung als ungenutzt erkannt hat, startet ausgewählt. Ein Ordner und die Dateien darin werden nicht doppelt gezählt.

## Korrekturen

- Jeder Cursor-Cache wird mit seinem eigenen Beleg angezeigt.
- Die Bereinigung lokaler Modelle listet nur Modelldateien, und nur wenn dieses Modellverzeichnis existiert.
- Vor dem Entfernen eines Worktrees wird erneut geprüft, ob der Ordner noch zu diesem Repository gehört.
