## Mejoras

- Reutiliza las descargas verificadas de actualizaciones nativas tras errores de instalación, comprobando de nuevo su integridad y eliminando los archivos rechazados.
- Separa la salida de los comandos nativos de los diagnósticos para mejorar el procesamiento de archivos y conservar los errores detallados de las tareas.
- Mejora los mensajes de error de actualización y la detección de la arquitectura en Mac Intel y Apple silicon.

## Problema conocido

- Un fallo SQLite SIGBUS observado en una validación anterior aún no se ha reproducido ni resuelto de forma concluyente.
