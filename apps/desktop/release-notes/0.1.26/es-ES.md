## Novedades

- La limpieza ahora puede borrar cachés regenerables, registros y descargas abandonadas de herramientas de programación con IA, como Codex, Claude Code, Cursor, Antigravity, Continue y opencode. Las sesiones, los recuerdos y las credenciales se conservan.
- En un proyecto que sigues usando, los archivos de compilación incremental se pueden quitar por separado. Eso solo ralentiza la siguiente compilación. Reconstruir todo el proyecto sigue siendo otra opción y empieza sin seleccionar.
- Los almacenes del historial de IA siguen siendo de solo lectura. Los archivos de transcripción con más de 30 días se pueden mover a la Papelera de uno en uno. La revisión muestra cuántas sesiones caen en 30, 90, 180 y 365 días, y las fechas más antigua y más reciente.
- Los pesos de modelo descargados de Ollama, LM Studio, Hugging Face, Jan y GPT4All se pueden mover a la Papelera. Los chats, los conjuntos de datos y los índices de documentos locales se conservan. Si esa herramienta está en ejecución, su almacén se deja en su sitio. MCP local no puede seleccionar estos elementos.
- Se listan los worktrees de Git y los clones duplicados con el espacio que ocupan. Un worktree enlazado solo se elimina cuando Git lo acepta, así que el trabajo sin confirmar se rechaza; la rama y el repositorio principal permanecen. Un clon duplicado se mueve a la Papelera solo si está limpio, completamente enviado y sin stash. Un worktree dañado se muestra y no se puede quitar desde aquí. Las filas que superan el límite de la lista se cuentan y no se muestran.

## Mejoras

- Un proyecto sin ediciones de código fuente durante 30 días puede ofrecer su resultado de compilación para limpiarlo. Un proyecto con ediciones recientes se considera activo, así que solo se ofrece la compilación incremental, más pequeña.
- Los archivos de los últimos 7 días siguen siendo de solo lectura. Un objetivo de 7 a 30 días se ofrece sin seleccionar. Solo un objetivo que la comprobación de actividad encontró inactivo empieza seleccionado. Una carpeta y los archivos que contiene no se cuentan dos veces.

## Correcciones

- Cada caché de Cursor se muestra con su propia evidencia.
- La limpieza de modelos locales lista solo archivos de modelo, y solo cuando existe ese directorio.
- Al quitar un worktree se vuelve a comprobar que la carpeta sigue perteneciendo a ese repositorio.
