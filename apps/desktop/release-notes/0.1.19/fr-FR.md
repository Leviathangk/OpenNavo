## Améliorations

- Réutilisation des téléchargements vérifiés des mises à jour natives après un échec d’installation, avec nouvelle vérification de l’intégrité et suppression des fichiers rejetés.
- Séparation de la sortie des commandes natives et des diagnostics pour améliorer le traitement des archives et conserver les erreurs détaillées des tâches.
- Amélioration des messages d’erreur de mise à jour et de la détection de l’architecture sur les Mac Intel et Apple silicon.

## Problème connu

- Un plantage SQLite SIGBUS observé lors d’une validation antérieure n’a pas encore été reproduit et résolu de façon concluante.
