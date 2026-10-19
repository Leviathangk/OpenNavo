## Novedades

- Añade un cortafuegos opcional para conexiones salientes, con reglas por app y destino, consultas de conexión, historial y listas de bloqueo. Actívalo en OpenNavo y aprueba la extensión del sistema y el filtro de red de macOS cuando se solicite.
- Ofrece controles y explicaciones del cortafuegos en los seis idiomas. Si no puede conectarse a la extensión, muestra el estado y si hace falta reiniciar.

## Mejoras

- Los paneles de la barra de menús se abren sin traer la ventana principal al frente y se cierran de forma más consistente.
- Mejora los mensajes sobre la cuenta del App Store e indica claramente si falta el componente de descarga.
- Los controles de inicio responden durante el análisis y los resultados antiguos no revierten los cambios.
- Protege los archivos SQLite en uso y sus alias frente al análisis de seguridad para conservar los bloqueos de la base de datos.
- Amplía las páginas de seguridad de la web y permite indexar las apps aptas pausadas únicamente por la regla de antigüedad del catálogo.

## Límites del cortafuegos

- Controla conexiones TCP y UDP salientes. Las conexiones desconocidas se permiten si la app no está disponible o la consulta caduca; no es una barrera que bloquee todo ante un fallo.
- Las reglas de dominio necesitan un nombre de host proporcionado por macOS. Sin él, se pueden aplicar reglas de IP, puerto y app.

## Problema conocido

- En una validación anterior se observó un fallo SQLite SIGBUS. Esta versión añade protección de bloqueos y pruebas de regresión, pero el fallo original aún no se ha reproducido y resuelto de forma concluyente.
