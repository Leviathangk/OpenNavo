## Novità

- Il monitor della barra dei menu può ora continuare a funzionare dopo l’uscita da OpenNavo. È attivo per impostazione predefinita e un avviso una tantum spiega dove disattivarlo; controlla lo stato in Impostazioni → Monitoraggio del sistema. Fai clic su un elemento della barra dei menu per vedere i valori senza aprire l’app. I valori delle ventole sono di sola lettura.
- Il MCP locale ora può accodare aggiornamenti per le app già elencate e pulire cache rigenerabili, sempre tramite una finestra di conferma e il registro attività locale. La finestra di conferma mostra anche il Team ID del richiedente.

## Miglioramenti

- Le azioni distruttive usano le finestre di conferma di OpenNavo: il piano viene ricontrollato prima dell’esecuzione, Consenti si attiva dopo una breve attesa e chiudere o lasciare la finestra equivale a negare.
- La pulizia non rimuove mai più di quanto mostrato. Gli elementi la cui dimensione non è stata misurata completamente appaiono non selezionati con l’indicazione «almeno».
- Gli avvisi di aggiornamento indicano solo gli aggiornamenti visibili e nuovi.
- Con la sola tastiera, Command-Return conferma mentre Consenti ha il focus; Return, Spazio ed Esc negano ancora.
- I link profondi aprono solo URL pubblici; le installazioni occupate e le conferme del firewall ora falliscono in sicurezza invece di riprovare o approvare.

## Correzioni

- Le conferme del MCP locale sono state rafforzate contro aggiramenti tramite navigazione, nuovo focus o crash, e le richieste negate restano nel registro.
- La segnalazione dell’utilizzo continua dopo un record rifiutato o un reindirizzamento.
- L’etichetta Bundle ID e gli avvisi sulle app spostate sono stati corretti in tutte le lingue.
