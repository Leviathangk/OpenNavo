## Neu

- Der Monitor in der Menüleiste kann nach dem Beenden von OpenNavo weiterlaufen. Er ist standardmäßig aktiviert, und ein einmaliger Hinweis erklärt, wo Sie ihn deaktivieren können; den Status sehen Sie unter Einstellungen → Systemmonitor. Klicken Sie auf ein Menüleisten-Element, um die Werte ohne Öffnen der App zu sehen. Lüfterwerte werden nur angezeigt.
- Lokales MCP kann jetzt Updates für bereits gelistete Apps einreihen und regenerierbare Caches bereinigen – immer über ein Bestätigungsfenster und das lokale Aktivitätsprotokoll. Das Bestätigungsfenster zeigt außerdem die Team ID des Aufrufers.

## Verbesserungen

- Destruktive Aktionen verwenden die Bestätigungsfenster von OpenNavo: Der Plan wird vor der Ausführung erneut geprüft, „Erlauben“ wird nach kurzer Verzögerung aktiv, und Schließen oder Verlassen des Fensters gilt als Ablehnen.
- Die Bereinigung entfernt nie mehr als in der Prüfung angezeigt. Elemente, deren Größe nicht vollständig gemessen werden konnte, erscheinen nicht ausgewählt und mit „mindestens“.
- Update-Hinweise nennen nur sichtbare, neue Updates.
- Bei reiner Tastaturbedienung bestätigt Command-Return, während „Erlauben“ den Fokus hat; Return, Leertaste und Escape lehnen weiterhin ab.
- Deep Links öffnen nur öffentliche URLs; eine gerade belegte Installation und Firewall-Bestätigungen schlagen jetzt sicher fehl, statt zu wiederholen oder zuzustimmen.

## Korrekturen

- Die Bestätigungen des lokalen MCP wurden gegen Umgehungen durch Navigation, erneuten Fokus oder Absturz der Seite gehärtet; abgelehnte Anfragen bleiben im Protokoll.
- Die Nutzungsmeldung läuft nach einem abgelehnten Datensatz oder einer Weiterleitung weiter.
- Die Bundle-ID-Bezeichnung und Warnungen zu verschobenen Apps wurden in allen Sprachen korrigiert.
