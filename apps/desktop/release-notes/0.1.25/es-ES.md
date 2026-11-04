## Novedades

- El monitor de la barra de menús ahora puede seguir funcionando después de salir de OpenNavo. Está activado de forma predeterminada y un aviso único explica dónde desactivarlo; consulta su estado en Ajustes → Monitor del sistema. Haz clic en un elemento de la barra de menús para ver sus lecturas sin abrir la app. Los valores de los ventiladores solo se muestran.
- El MCP local ahora puede poner en cola actualizaciones de las apps que ya lista y limpiar cachés regenerables, siempre mediante una ventana de confirmación y el registro de actividad local. La ventana de confirmación también muestra el Team ID del solicitante.

## Mejoras

- Las acciones destructivas usan las ventanas de confirmación de OpenNavo: el plan se comprueba de nuevo antes de ejecutarse, Permitir se habilita tras una breve espera y cerrar o salir de la ventana equivale a denegar.
- La limpieza nunca elimina más de lo que mostró la revisión. Los elementos cuyo tamaño no se pudo medir por completo aparecen sin seleccionar y con la indicación «al menos».
- Los avisos de actualización solo nombran las actualizaciones visibles y nuevas.
- Con solo el teclado, puedes confirmar con Command-Return mientras Permitir tiene el foco; Return, Espacio y Escape siguen denegando.
- Los enlaces profundos solo abren URL públicas; las instalaciones ocupadas y las confirmaciones del cortafuegos fallan de forma segura en lugar de reintentar o aprobar.

## Correcciones

- Se reforzaron las confirmaciones del MCP local contra omisiones por navegación, cambio de foco o bloqueo, y las solicitudes denegadas se conservan en el registro.
- El registro de uso continúa tras un registro rechazado o una redirección.
- Se corrigieron la etiqueta Bundle ID y los avisos de apps movidas en todos los idiomas.
