## Mejoras

- Reduce el trabajo recurrente de CPU, sistema de archivos y base de datos en los análisis de seguridad en segundo plano, la actividad del cortafuegos y la supervisión de ventiladores, manteniendo las señales de conexión cada cinco segundos.
- Reintenta las comprobaciones de inicio rechazadas en análisis posteriores, detecta sustituciones atómicas de descargas y mantiene el intervalo de sondeo alternativo si se desconectan los eventos del sistema de archivos.
- Mejora el muestreo del monitor de recursos, las lecturas de procesos y la actualización de gráficos.
- Mejora la gestión de firmas de paquetes, rutas de instalación y solicitudes de administrador en instalaciones de App Store.
- Recopila automáticamente datos anónimos de uso diario, incluso en segundo plano. Los informes contienen un identificador aleatorio de instalación, hora, plataforma, versión de la aplicación e idioma; se ha eliminado el interruptor anterior de Ajustes.

## Problema conocido

- Un fallo SQLite SIGBUS observado durante una validación anterior aún no se ha reproducido y resuelto de forma concluyente.
