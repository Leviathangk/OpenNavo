## Nouveautés

- Le moniteur de la barre des menus peut désormais continuer à fonctionner après avoir quitté OpenNavo. Il est activé par défaut et un avis unique indique où le désactiver ; consultez son état dans Réglages → Surveillance du système. Cliquez sur un élément de la barre des menus pour voir ses relevés sans ouvrir l’app. Les valeurs des ventilateurs sont en lecture seule.
- Le MCP local peut maintenant mettre en file les mises à jour des apps qu’il liste déjà et nettoyer les caches régénérables, toujours via une fenêtre de confirmation et le journal d’activité local. La fenêtre de confirmation affiche aussi le Team ID de l’appelant.

## Améliorations

- Les actions destructrices utilisent les fenêtres de confirmation d’OpenNavo : le plan est revérifié avant exécution, Autoriser devient disponible après un court délai, et fermer ou quitter la fenêtre équivaut à refuser.
- Le nettoyage ne supprime jamais plus que ce qui a été affiché. Les éléments dont la taille n’a pas pu être mesurée complètement apparaissent non sélectionnés avec la mention « au moins ».
- Les notifications de mise à jour ne nomment que les mises à jour visibles et nouvelles.
- Au clavier seul, Command-Return confirme lorsque Autoriser a le focus ; Return, Espace et Échap refusent toujours.
- Les liens profonds n’ouvrent que des URL publiques ; les installations occupées et les confirmations du pare-feu échouent désormais en sécurité au lieu de réessayer ou d’approuver.

## Corrections

- Les confirmations du MCP local ont été renforcées contre les contournements par navigation, changement de focus ou plantage, et les demandes refusées restent dans le journal.
- Le rapport d’utilisation continue après un enregistrement refusé ou une redirection.
- Le libellé Bundle ID et les avertissements d’apps déplacées ont été corrigés dans toutes les langues.
