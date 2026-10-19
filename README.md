# clon

Entorno de desarrollo local para Windows, pensado sobre todo para Laravel e inspirado en Laragon. Escrito en Go. No está afiliado a Laragon ni a sus autores.

**Estado:** fase 1 (núcleo) funcionando, probada en Windows 11 junto a una instalación de Laragon y de punta a punta en un Windows limpio (Windows Sandbox).

## Qué hace hoy

- **Sitios `.test` automáticos con HTTPS.**
  - Cada carpeta de tus raíces de proyectos se publica como `https://<carpeta>.test`.
  - Las carpetas nuevas aparecen solas, sin cortar Apache.
  - Los nombres raros se normalizan: `mi proyecto 2` → `mi-proyecto-2.test`.
- **Proxy frontal propio (80/443).** Presenta un certificado por sitio, firmado por una CA local, y redirige HTTP a HTTPS. Los sitios desconocidos reciben una página de error útil.
- **PHP global con cambio en caliente.** Funciona con cualquier versión, incluida la 7.2. PHP corre por FastCGI (un pool de `php-cgi`), así que Apache no se reinicia al cambiarlo, y la terminal, Composer y Artisan siguen al mismo PHP.
- **Apache 2.4** con un puerto interno por sitio y `.htaccess` (`AllowOverride All`). PHP recibe `HTTPS=on` y las URLs correctas.
- **MySQL y MariaDB, varias versiones a la vez.** La pestaña «Servicios» del panel (o `clon service`) instala versiones desde un catálogo de descargas oficiales:
  - MySQL de cdn.mysql.com, con la firma de Oracle comprobada.
  - MariaDB de mariadb.org, con su SHA-256 comprobado.

  Cada versión tiene su puerto, su carpeta de datos y su interruptor. Una por tipo es la predeterminada: usa el 3306, arranca con «Iniciar todo» y es la que abre el atajo `mysql`. También puede usar las bases de una instalación de Laragon sin copiarlas (`clon mysql laragon-data`), sin abrirlas nunca a la vez que Laragon.
- **Node incluido:** descarga la LTS oficial, verificada, con `npm` y `npx`. Los paquetes globales de npm sobreviven a los cambios de versión.
- **Composer** actualizado y atajos de terminal (`php`, `php84`, `php83`…, `composer`, `node`, `npm`, `npx`, `mysql`) en una sola carpeta `shims\`. Los procesos PHP que lanzan Composer o Artisan heredan el mismo `php.ini`.
- **Importación desde Laragon.** Usa en su sitio, sin copiar ni modificar, sus versiones de PHP, Apache, MySQL, Nginx, Node y Redis, y sus carpetas de proyectos.
- **Supervisor.** Los servicios corren ocultos dentro de *job objects*: si clon muere, Windows los cierra todos. Si uno se cae, se reinicia, y el apagado normal es ordenado.
- **Bandeja, panel web y línea de comandos.** El panel se abre en `https://localhost` (o en la dirección local de control mientras la CA no es de confianza) y su API está protegida con token, verificación de `Host` y de `Origin`.

## Requisitos

- **Windows 10 u 11 de 64 bits.**
- **Runtime de Visual C++ 2015–2022 x64 de Microsoft.** Lo necesitan los binarios de PHP, Apache y MySQL para Windows. Si falta, `clon setup` (o `clon vcredist install`) instala la versión oficial de Microsoft tras comprobar su firma; Windows pide permiso de administrador. En un Windows recién instalado, el instalador de Microsoft tarda unos 4 minutos.
- **Go 1.27 o posterior,** solo para compilar.
- **PHP, Node y Composer:** clon los descarga solo.
- **MySQL y MariaDB:** se instalan desde el panel o con `clon service install`; también se importan de Laragon.
- **Apache:** por ahora sale de una instalación de Laragon (`clon import laragon`). La descarga directa está en la hoja de ruta.

## Compilar

```powershell
go test ./...
go build -o C:\clon\clon.exe ./cmd/clon
go build -o C:\clon\clon-shim.exe ./cmd/clon-shim
go build -ldflags "-H=windowsgui" -o C:\clon\clonw.exe ./cmd/clonw
```

Los tres ejecutables van juntos en la carpeta de instalación, que no debe tener espacios en la ruta:
- `clon.exe`: la línea de comandos.
- `clonw.exe`: el programa con la bandeja.
- `clon-shim.exe`: la plantilla de los atajos.

Todo lo que clon descarga o genera queda dentro de esa carpeta. Para usar otra carpeta, define `CLON_HOME`.

## Primer uso

```powershell
C:\clon\clon.exe setup     # importa Laragon si existe, instala Node LTS (con npm y npx), Composer y la lista de CAs públicas
C:\clon\clon.exe run       # arranca clon con el icono en la bandeja
C:\clon\clon.exe status    # servicios, sitios y avisos
```

Para usarlo a la vez que Laragon, que ocupa los puertos 80, 443, 3306 y 6379, elige otros puertos para el proxy en `C:\clon\config\clon.toml`, y para las bases de datos en el panel o con `clon service port mysql-8.0 3307`:

```toml
[proxy]
  http_port = 8081
  https_port = 8444
```

## Pasar de Laragon a clon

1. Cierra Laragon.
2. En `config\clon.toml`, deja `[proxy] http_port = 80` y `https_port = 443`. Tu MySQL predeterminado debe usar el 3306 (`clon service list` lo muestra).
3. `clon mysql laragon-data`: usa tus bases de datos de Laragon sin copiarlas.
4. `clon ca trust`: Windows te pide confirmar la CA local, solo para tu usuario.
5. `clon hosts sync`: Windows pide permiso de administrador para escribir el archivo `hosts`.
6. `clon path add` (opcional): `php`, `composer`, `node` y `npm` del clon en cualquier terminal.
7. `clon run` y abre `https://<tu-proyecto>.test`.

Para volver a Laragon: `clon quit` y abre Laragon. Los dos usan los mismos datos, nunca a la vez.

## Comandos

| Comando | Qué hace |
|---|---|
| `clon run [--foreground]` | Inicia clon (bandeja), o en la consola actual con `--foreground` |
| `clon start` · `stop` · `restart` · `quit` | Servicios on/off; `quit` cierra clon |
| `clon status` · `sites` · `versions` | Estado, sitios y versiones instaladas |
| `clon php list` · `remote` · `install 8.4` · `use 8.3` | Versiones de PHP |
| `clon node list` · `remote` · `install lts` · `use 24` | Versiones de Node (con npm y npx) |
| `clon composer install` | Descarga o actualiza Composer |
| `clon vcredist status` · `install [instalador.exe]` | Runtime de Visual C++ que necesitan PHP, Apache y MySQL |
| `clon import laragon [ruta]` | Importa versiones y carpetas de Laragon |
| `clon service list` · `remote` · `install mysql 8.4` | Bases de datos instaladas · catálogo · instalar una versión |
| `clon service start` · `stop` · `default` · `uninstall mysql-8.4` | Una instancia: iniciar, detener, hacerla predeterminada o quitarla (los datos se conservan) |
| `clon service port mysql-8.4 3307` · `autostart mysql-8.4 on` | Su puerto y si arranca con «Iniciar todo» |
| `clon mysql laragon-data` · `own-data` | Usa las bases de Laragon o la carpeta de datos propia |
| `clon ca status` · `trust` · `untrust` | CA local para HTTPS |
| `clon hosts status` · `sync` | Bloque gestionado del archivo `hosts` |
| `clon path add` · `remove` | Atajos de clon en el PATH de tu usuario |

`CLON_APACHE_LOGLEVEL="warn proxy_fcgi:trace8"` hace que Apache registre las variables que envía a PHP (útil para diagnosticar).

## Probar en un Windows limpio (Windows Sandbox)

```powershell
.\scripts\sandbox\probar-en-sandbox.ps1
```

Compila clon y abre Windows Sandbox. Comparte en solo lectura los ejecutables de clon y `C:\laragon\bin`. Dentro del sandbox recorre todo el flujo real:
- Runtime de Visual C++, importación de Laragon y `clon setup`.
- PHP descargado de windows.php.net.
- Confiar en la CA y escribir el archivo `hosts`.
- Puertos 80/443.
- Proyecto Laravel nuevo con MySQL y `npm run build`.
- MariaDB instalada desde el catálogo y corriendo a la vez que MySQL.
- Edge sin avisos, cambio de PHP y cierre.

Tarda unos 10 minutos. No cierres la ventana del sandbox hasta que diga «Prueba terminada»: al cerrarla se descarta todo lo de dentro. Al terminar, clon queda arrancado con el proyecto Laravel y el panel abiertos en Edge, para probarlo a mano. El informe queda en `dist\sandbox\<fecha>\resultados\report.md`. El instalador de Visual C++ se descarga en el equipo anfitrión (`dist\cache`) y se comparte con el sandbox, porque dentro su descarga resultó poco fiable.

## Estructura

```text
cmd/clon         línea de comandos            internal/proxy       proxy frontal HTTPS
cmd/clonw        bandeja (sin consola)        internal/ca          CA local y certificados
cmd/clon-shim    atajos php/node/npm…         internal/sites       detección de sitios y puertos
internal/app     servicio principal y API     internal/hosts       bloque del archivo hosts
internal/apache  httpd.conf y control         internal/supervisor  procesos y job objects
internal/php     php.ini y pool de php-cgi    internal/ports       puertos libres y reservados
internal/mysql   MySQL y MariaDB: my.ini      internal/install     descargas oficiales verificadas
internal/laragon importación de Laragon       internal/versions    catálogo de versiones
```

## Probado (octubre de 2026)

En Windows 11, junto a Laragon y en puertos alternativos:

- **PHP:**
  - 8.4, 8.3 y 7.2 por HTTPS, con rutas tipo Laravel a través del `.htaccess`, `HTTPS=on` y las URLs correctas.
  - Conexión a MySQL y OPcache activo.
  - 20 peticiones en paralelo devuelven 200 en menos de 0,5 s.
  - El cambio de PHP en caliente tarda entre 0,4 y 1,3 s.
- **Apache:** una carpeta nueva se publica en segundos con recarga elegante (mismo PID), y las redirecciones de Apache conservan `https` y el puerto.
- **Supervisor:** un `php-cgi` que se cae se reinicia solo. Si se mata `clon.exe` de golpe, no quedan procesos huérfanos. El apagado es ordenado (Apache con su señal, MySQL con `mysqladmin`).
- **Panel:** sin token responde 403, y con otro `Host` u `Origin` no es alcanzable.
- **Laragon:** clon se niega a abrir los datos de MySQL de Laragon mientras Laragon los usa.
- **Node y atajos:** Node 24 LTS descargado y verificado; `npm`, `npx`, `php` y `composer` funcionan desde `shims\`.
- **Bases de datos:**
  - Funcionan a la vez MySQL 8.0 (de Laragon), MySQL 8.4 y MariaDB 11.8 (descargadas), cada una en su puerto.
  - Al cambiar la predeterminada, el atajo `mysql` la sigue.
  - «Iniciar todo» arranca solo las predeterminadas y las marcadas.
  - Cancelar una descarga no deja restos. Desinstalar borra los programas descargados, conserva los datos y nunca toca los archivos de Laragon.

En un Windows limpio (Windows Sandbox), con los puertos 80 y 443, pasan los 24 pasos de `probar-en-sandbox.ps1`:

- Instalación del runtime de Visual C++ y PHP descargado de windows.php.net.
- Proyecto Laravel 13 nuevo con MySQL, migraciones y `npm run build`.
- Archivo `hosts` y CA local de confianza: Edge abre los sitios sin avisos de certificado.
- Cambio de PHP en caliente, panel, atajos y cierre sin procesos sueltos.

## Hoja de ruta

- **Fase 1 (núcleo), pendiente:** descarga directa de Apache.
- **Fase 2, en este orden:**
  - Hecho: sección «Servicios» en el panel; MySQL y MariaDB en varias versiones a la vez.
  - Nginx por sitio.
  - FrankenPHP por sitio.
  - MongoDB, PostgreSQL, Redis, Mailpit y Meilisearch.
  - Varias versiones de Apache, incluidos perfiles legacy con su propio PHP.
- **Fase 3:**
  - Instaladores: Laravel con sus starter kits, WordPress, Symfony, desde Git y recetas propias.
  - `.env` automático y procesos del proyecto (cola, scheduler, Reverb, Vite).
  - Vite con HTTPS, Xdebug por sitio y compartir con ngrok.
- **Fase 4:** DNS comodín para `*.sitio.test`, modo worker de FrankenPHP, actualizador y firma de código.

## Licencia

[MIT](LICENSE)
