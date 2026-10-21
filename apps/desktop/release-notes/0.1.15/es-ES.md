## Mejoras

- Añade estadísticas de uso anónimas y opcionales de las instalaciones de Mac en ejecución, incluido el funcionamiento en segundo plano. Están desactivadas por defecto y se pueden activar en Ajustes; solo se envían un identificador aleatorio, la hora, la plataforma, la versión de la aplicación y el idioma. Desactivarlas elimina el identificador local y los informes pendientes.
- Comprueba las actualizaciones de OpenNavo cada hora cuando las comprobaciones automáticas están activadas, en vez de detenerlas tras la primera comprobación correcta del día.
- Evita que la actualización de información de Homebrew al iniciar impida las comprobaciones programadas de las fuentes de actualización seleccionadas.

## Problema conocido

- Un fallo SQLite SIGBUS observado durante una validación anterior aún no se ha reproducido ni resuelto de forma concluyente.
