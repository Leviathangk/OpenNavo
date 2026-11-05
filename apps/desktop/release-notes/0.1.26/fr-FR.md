## Nouveautés

- Le nettoyage peut maintenant supprimer les caches régénérables, les journaux et les téléchargements abandonnés des outils de programmation IA, dont Codex, Claude Code, Cursor, Antigravity, Continue et opencode. Les sessions, les mémoires et les identifiants restent.
- Dans un projet que vous utilisez encore, les fichiers de compilation incrémentale peuvent être retirés seuls. Cela ne fait que ralentir la compilation suivante. Reconstruire tout le projet reste un choix distinct et commence non sélectionné.
- Les stockages d’historique IA restent en lecture seule. Les fichiers de transcription de plus de 30 jours peuvent être déplacés vers la Corbeille un par un. La vérification indique combien de sessions se situent dans 30, 90, 180 et 365 jours, ainsi que les dates la plus ancienne et la plus récente.
- Les poids de modèles téléchargés pour Ollama, LM Studio, Hugging Face, Jan et GPT4All peuvent être déplacés vers la Corbeille. Les conversations, les jeux de données et les index de documents locaux restent. Si cet outil est en cours d’exécution, son stockage est laissé en place. Le MCP local ne peut pas sélectionner ces éléments.
- Les worktrees Git et les clones en double sont listés avec l’espace qu’ils occupent. Un worktree lié n’est retiré que si Git l’accepte, donc le travail non validé est refusé ; la branche et le dépôt principal restent. Un clone en double va dans la Corbeille seulement s’il est propre, entièrement poussé et sans stash. Un worktree cassé est affiché et ne peut pas être retiré ici. Les lignes au-delà de la limite de liste sont comptées, pas affichées.

## Améliorations

- Un projet sans modification des sources depuis 30 jours peut proposer son résultat de compilation au nettoyage. Un projet modifié récemment reste actif, donc seule la compilation incrémentale, plus petite, est proposée.
- Les fichiers des 7 derniers jours restent en lecture seule. Une cible de 7 à 30 jours est proposée non sélectionnée. Seule une cible que le contrôle d’activité a jugée inactive commence sélectionnée. Un dossier et les fichiers qu’il contient ne sont pas comptés deux fois.

## Corrections

- Chaque cache Cursor est affiché avec sa propre preuve.
- Le nettoyage des modèles locaux ne liste que les fichiers de modèle, et seulement si ce dossier existe.
- Retirer un worktree vérifie à nouveau que le dossier appartient encore à ce dépôt.
