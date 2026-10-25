## Mejoras

- Evita que las lecturas de la biblioteca, las preferencias de actualización y la actualización de la bandeja bloqueen la ventana al esperar acceso al disco o bloqueos.
- Vuelve a comprobar los candidatos de actualización nativa obsoletos tras reiniciar o volver a montar, sin omitir comprobaciones programadas ni actualizaciones automáticas.
- Impide que respuestas antiguas sobrescriban estados más recientes de tareas, actualizaciones y cortafuegos; conserva los cambios en reglas y listas de bloqueo durante la carga.
- Mejora los textos en chino tradicional y reduce las consultas repetidas de fuentes de actualización al leer el inventario de aplicaciones.

## Problema conocido

- Un fallo SQLite SIGBUS observado en una validación anterior aún no se ha reproducido ni resuelto de forma concluyente.
