# clon

Entorno de desarrollo local para Windows, pensado sobre todo para Laravel e inspirado en Laragon. Escrito en Go. No está afiliado a Laragon ni a sus autores.

**Estado:** fases 1 (núcleo) y 2 (versiones y servicios) completas. Probado en Windows 11 junto a una instalación de Laragon y de punta a punta en un Windows limpio (Windows Sandbox).

## Qué hace hoy

- **Proyectos Laravel nuevos en un paso:** botón «Nuevo proyecto» en la pestaña «Sitios», o `clon new tienda --kit react --db mysql`.
  - Laravel sin kit o con los kits oficiales: React, Vue, Svelte o Livewire. Se crean como con el instalador de Laravel, incluidos los pasos propios de cada kit.
  - clon escribe el `.env` (nombre, `APP_URL` en https, la base de datos elegida y el correo a Mailpit), crea la base en MySQL, MariaDB o PostgreSQL (o usa SQLite) y migra.
  - Con un kit, también instala las dependencias de npm y compila el frontend con Vite.
  - El sitio queda en `https://tienda.test`. El panel muestra cada fase y se puede cancelar.
- **Procesos de cada proyecto:** columna «Procesos» de la pestaña «Sitios», o `clon site proc tienda vite on`.
  - Vite (`npm run dev`), la cola (`queue:work`) y las tareas programadas (`schedule:work`) corren supervisados, con su registro en `logs\`. Los que dejas encendidos arrancan con «Iniciar todo».
  - Vite va por HTTPS con el certificado del sitio, en el que Windows ya confía: la página y la recarga en caliente usan `https://tienda.test:5173`, sin tocar `vite.config`.
  - clon escribe también `VITE_DEV_SERVER_KEY` y `VITE_DEV_SERVER_CERT` en el `.env`, así `npm run dev` en una terminal también sirve HTTPS. Al apagar Vite, clon borra `public/hot`.
  - Vite necesita que el sitio esté en el archivo `hosts`; si falta, clon lo avisa y lo arranca al actualizar `hosts`.
  - La cola y las tareas usan el PHP del sitio (el fijado o el global) y se reinician al cambiarlo.
- **Xdebug por sitio:** botón «Xdebug» junto al PHP del sitio, o `clon site debug tienda on`.
  - Ese sitio pasa a un grupo de `php-cgi` de su versión con Xdebug, que se conecta al editor (VS Code, PhpStorm) en 127.0.0.1:9003. Los demás siguen igual de rápidos, sin Xdebug.
  - Si a esa versión le falta Xdebug, clon lo descarga.
  - `phpx` es el PHP global con Xdebug, para depurar Artisan y los tests desde la terminal; `php` sigue sin él.
- **Sitios `.test` automáticos con HTTPS.**
  - Cada carpeta de tus raíces de proyectos se publica como `https://<carpeta>.test`.
  - Las carpetas nuevas aparecen solas, sin cortar Apache.
  - Los nombres raros se normalizan: `mi proyecto 2` → `mi-proyecto-2.test`.
- **Proxy frontal propio (80/443).** Presenta un certificado por sitio, firmado por una CA local, y redirige HTTP a HTTPS. Los sitios desconocidos reciben una página de error útil.
- **PHP global con cambio en caliente.** Funciona con cualquier versión, incluida la 7.2. PHP corre por FastCGI (un pool de `php-cgi`), así que Apache no se reinicia al cambiarlo, y la terminal, Composer y Artisan siguen al mismo PHP.
  - Excepción para proyectos antiguos: un sitio puede fijar su propia versión en la pestaña «Sitios» o con `clon site php tienda 7.2`.
  - Ese sitio corre en un grupo de `php-cgi` propio, con Apache o Nginx, y sigue igual aunque cambies el PHP global.
  - Dentro de su carpeta, `php`, `composer` y `artisan` usan esa versión. Si es anterior a 7.2.5, clon usa Composer 2.2 LTS, el último que la admite.
  - PHP 5.x necesita además el runtime de Visual C++ de su época (2012 para PHP 5.5 y 5.6).
- **Extensiones de PHP con casillas,** como en el menú de Laragon: pestaña «PHP y Node» o `clon php ext on soap`, por versión de PHP.
  - Xdebug y MongoDB se descargan de sus repositorios oficiales: las compilaciones para Windows que publican para PIE, con su SHA-256.
  - Se guardan en una carpeta propia de clon, sin tocar los PHP de Laragon.
  - Antes de aplicar un cambio, clon comprueba que PHP cargue la extensión; si no carga (por ejemplo, le falta una DLL), no cambia nada y muestra el aviso de PHP.
  - Los procesos de esa versión se reinician solos.
  - phpredis y APCu no publican compilaciones para Windows; Laravel funciona igual con `predis` y la caché en archivos.
- **Apache 2.4** con un puerto interno por sitio y `.htaccess` (`AllowOverride All`). PHP recibe `HTTPS=on` y las URLs correctas.
  - Se descarga de Apache Lounge (la compilación para Windows que usa también Laragon) con su SHA-256 comprobado, o se importa de Laragon. Apache Lounge mantiene esas compilaciones de forma voluntaria y acepta donaciones en apachelounge.com.
  - Puedes tener varias versiones: el panel (o `clon apache use 2.4.62`) cambia en caliente la que atiende los sitios.
- **Nginx o FrankenPHP por sitio.** Cada sitio elige Apache, Nginx o FrankenPHP en la pestaña «Sitios» (o con `clon site server tienda nginx`), sin cambiar su dirección `.test`.
  - Ambos se instalan desde el catálogo: los repositorios oficiales github.com/nginx/nginx y github.com/php/frankenphp, con el SHA-256 comprobado.
  - Solo arrancan cuando algún sitio los elige, y se recargan en caliente.
  - Nginx usa el PHP global; FrankenPHP usa su propio PHP (8.5 en la 1.13), con un `php.ini` que genera clon.
- **MySQL y MariaDB, varias versiones a la vez.** La pestaña «Servicios» del panel (o `clon service`) instala versiones desde un catálogo de descargas oficiales:
  - MySQL de cdn.mysql.com, con la firma de Oracle comprobada.
  - MariaDB de mariadb.org, con su SHA-256 comprobado.

  Cada versión tiene su puerto, su carpeta de datos y su interruptor. Una por tipo es la predeterminada: usa el 3306, arranca con «Iniciar todo» y es la que abre el atajo `mysql`. También puede usar las bases de una instalación de Laragon sin copiarlas (`clon mysql laragon-data`), sin abrirlas nunca a la vez que Laragon.
- **PostgreSQL, MongoDB, Redis, Mailpit y Meilisearch.** Se instalan desde el mismo catálogo, cada versión con su puerto, sus datos y su interruptor; la predeterminada usa el puerto estándar (5432, 27017, 6379, 1025 y 7700).
  - **PostgreSQL:** zip oficial de get.enterprisedb.com, enlazado desde postgresql.org, sin pgAdmin. EnterpriseDB no publica sumas ni firma esos programas, así que clon comprueba que el zip esté íntegro y que traiga la versión esperada. El servidor corre sin permisos de administrador (PostgreSQL se niega a usarlos) y se detiene con `pg_ctl`. Atajo `psql`.
  - **MongoDB:** desde mongodb.org con su SHA-256, junto con `mongosh`, que sirve para detenerlo de forma ordenada y queda como atajo.
  - **Redis:** Redis no publica versión para Windows; clon usa la compilación comunitaria github.com/redis-windows (con su SHA-256) o el Redis de Laragon. Atajo `redis-cli`.
  - **Mailpit:** recibe lo que envían `mail()` de PHP y Laravel (SMTP en el 1025) y lo muestra en su página (8025).
  - **Meilisearch:** para Laravel Scout, en modo desarrollo y con su panel en el navegador.
  - Lo que se importa de Laragon aparece en el panel pero no arranca solo; lo que se instala desde el catálogo pasa a ser el predeterminado de su tipo.
  - «Copiar datos de conexión» da las líneas del `.env` de Laravel para cada tipo.
- **Adminer en `https://db.localhost`** para ver y editar las bases de datos en el navegador (MySQL, MariaDB, PostgreSQL y SQLite).
  - Se instala desde el catálogo (github.com/vrana/adminer, con su SHA-256) y corre con el PHP global.
  - Entra sin contraseña con root o postgres, y solo responde a este equipo, aunque el proxy se abra a la red local.
  - Cada base de datos del panel tiene «Abrir en Adminer» en su menú.
- **Node incluido:** descarga la LTS oficial, verificada, con `npm` y `npx`. Los paquetes globales de npm sobreviven a los cambios de versión.
- **Composer** actualizado y atajos de terminal (`php`, `php84`, `php83`…, `composer`, `node`, `npm`, `npx`, `mysql`) en una sola carpeta `shims\`. Los procesos PHP que lanzan Composer o Artisan heredan el mismo `php.ini`.
- **Importación desde Laragon.** Usa en su sitio, sin copiar ni modificar, sus versiones de PHP, Apache, MySQL, Nginx, Node y Redis, y sus carpetas de proyectos.
- **Supervisor.** Los servicios corren ocultos dentro de *job objects*: si clon muere, Windows los cierra todos. Si uno se cae, se reinicia, y el apagado normal es ordenado.
- **Bandeja, panel web y línea de comandos.** El panel se abre en `https://localhost` (o en la dirección local de control mientras la CA no es de confianza) y su API está protegida con token, verificación de `Host` y de `Origin`.

## Requisitos

- **Windows 10 u 11 de 64 bits.**
- **Runtime de Visual C++ x64 de Microsoft** (v14, el de Visual Studio 2015 a 2026). Lo necesitan los binarios de PHP, Apache y MySQL para Windows.
  - Si falta, `clon setup` (o `clon vcredist install`, o el botón del panel) instala la versión oficial más reciente tras comprobar su firma de Microsoft. Windows pide permiso de administrador. En un Windows recién instalado, el instalador de Microsoft tarda unos 4 minutos.
  - El Apache actual de Apache Lounge se compila con Visual Studio 2026, y Microsoft pide para él el runtime 14.50 o posterior. Funciona igual con los anteriores (probado con 14.44 y 14.32), así que clon no bloquea nada: solo avisa y ofrece actualizarlo. Si otros programas tienen abierto el runtime, Windows termina de actualizarlo al reiniciar, y el aviso lo indica.
- **Go 1.27 o posterior,** solo para compilar.
- **PHP, Apache, Node y Composer:** clon los descarga solo. Sin Laragon, `clon setup` instala la versión más reciente de PHP y de Apache.
- **Bases de datos y servicios** (MySQL, MariaDB, PostgreSQL, MongoDB, Redis, Mailpit, Meilisearch): se instalan desde el panel o con `clon service install`. MySQL y Redis también se importan de Laragon.

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
C:\clon\clon.exe setup     # importa Laragon si existe (si no, descarga PHP y Apache), instala Node LTS (con npm y npx), Composer y la lista de CAs públicas
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
| `clon new tienda [--kit react\|vue\|svelte\|livewire] [--db sqlite\|mysql\|mariadb\|postgresql]` | Proyecto Laravel nuevo en `https://tienda.test` |
| `clon start` · `stop` · `restart` · `quit` | Servicios on/off; `quit` cierra clon |
| `clon status` · `sites` · `versions` | Estado, sitios y versiones instaladas |
| `clon php list` · `remote` · `install 8.4` · `use 8.3` | Versiones de PHP |
| `clon php ext [8.4]` · `ext on xdebug` · `ext off exif [8.3]` | Extensiones de cada versión de PHP |
| `clon apache list` · `remote` · `install` · `use 2.4.62` | Versiones de Apache (descarga de Apache Lounge) |
| `clon node list` · `remote` · `install lts` · `use 24` | Versiones de Node (con npm y npx) |
| `clon composer install` | Descarga o actualiza Composer |
| `clon vcredist status` · `install [instalador.exe]` | Runtime de Visual C++ que necesitan PHP, Apache y MySQL |
| `clon import laragon [ruta]` | Importa versiones y carpetas de Laragon |
| `clon service list` · `remote` · `install postgresql latest` | Servicios instalados · catálogo · instalar (mysql, mariadb, postgresql, mongodb, redis, mailpit, meilisearch) |
| `clon service start` · `stop` · `default` · `uninstall mysql-8.4` | Una instancia: iniciar, detener, hacerla predeterminada o quitarla (los datos se conservan) |
| `clon service port mysql-8.4 3307` · `autostart mysql-8.4 on` | Su puerto y si arranca con «Iniciar todo» |
| `clon mysql laragon-data` · `own-data` | Usa las bases de Laragon o la carpeta de datos propia |
| `clon site server tienda nginx` · `frankenphp` · `apache` | Servidor web de un sitio |
| `clon site php tienda 7.2` · `global` | PHP propio para un proyecto antiguo, o vuelta al global |
| `clon site proc tienda vite\|queue\|schedule on\|off` | Procesos del proyecto: Vite con HTTPS, cola y tareas programadas |
| `clon site debug tienda on\|off` · `phpx` | Xdebug solo para ese sitio · PHP con Xdebug en la terminal |
| `clon service install nginx latest` · `frankenphp 1.13` | Instala un servidor web desde el catálogo |
| `clon service install adminer latest` | Adminer en `https://db.localhost` |
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
- PHP 7.2 fijado en un sitio, en la web y en la terminal, mientras el resto usa el global.
- Extensiones de PHP: Xdebug descargado y soap, activadas y apagadas.
- Adminer en `https://db.localhost`, con entrada sin contraseña en MySQL.
- Proyecto nuevo con `clon new` (kit de Vue y MySQL) y los procesos de otro: Vite por HTTPS, cola y tareas.
- Xdebug solo en un sitio, y `phpx` en la terminal.
- Apache de Apache Lounge, cambio de versión en caliente y actualización de Visual C++ con clon en marcha.
- Edge sin avisos, cambio de PHP y cierre.

Tarda unos 20 minutos. No cierres la ventana del sandbox hasta que diga «Prueba terminada»: al cerrarla se descarta todo lo de dentro. Al terminar, clon queda arrancado con el proyecto Laravel y el panel abiertos en Edge, para probarlo a mano. El informe queda en `dist\sandbox\<fecha>\resultados\report.md`. Los instaladores de Visual C++ se descargan en el equipo anfitrión (`dist\cache`) y se comparten con el sandbox, porque dentro su descarga resultó poco fiable. Primero se instala a propósito el de Visual Studio 2022 (14.44) y después el más reciente.

## Estructura

```text
cmd/clon         línea de comandos            internal/proxy       proxy frontal HTTPS
cmd/clonw        bandeja (sin consola)        internal/ca          CA local y certificados
cmd/clon-shim    atajos php/node/npm…         internal/sites       detección de sitios y puertos
internal/app     servicio principal y API     internal/hosts       bloque del archivo hosts
internal/apache  httpd.conf y control         internal/supervisor  procesos y job objects
internal/php     php.ini y pool de php-cgi    internal/ports       puertos libres y reservados
internal/mysql   MySQL y MariaDB: my.ini      internal/install     descargas oficiales verificadas
internal/nginx   nginx.conf y control         internal/frankenphp  Caddyfile y control
internal/mongodb mongod y parada con mongosh  internal/postgres    initdb, servidor sin admin
internal/redis   redis.conf y redis-cli       internal/mailpit, internal/meilisearch
internal/laragon importación de Laragon       internal/versions    catálogo de versiones
```

## Probado (octubre de 2026)

En Windows 11, junto a Laragon y en puertos alternativos:

- **PHP:**
  - 8.4, 8.3 y 7.2 por HTTPS, con rutas tipo Laravel a través del `.htaccess`, `HTTPS=on` y las URLs correctas.
  - Conexión a MySQL y OPcache activo.
  - 20 peticiones en paralelo devuelven 200 en menos de 0,5 s.
  - El cambio de PHP en caliente tarda entre 0,4 y 1,3 s.
  - Un sitio fijado en PHP 7.2 responde con 7.2 (con MySQL y OPcache) mientras el resto usa 8.4. Sigue en 7.2 al cambiar el PHP global y al pasar a Nginx, y cada cambio rige en cuanto el comando responde, en 1 s aproximadamente.
  - En su carpeta, la terminal usa PHP 7.2 y Composer 2.2 LTS; fuera, el PHP global.
  - Extensiones: Xdebug 3.5 y MongoDB 2.5 se descargan y activan en 2-3 s, y ldap y soap, que vienen con PHP, también se activan; la web las ve en cuanto responde la orden. Una extensión a la que le falta una DLL (pdo_firebird) se rechaza sin tocar nada.
- **Apache:**
  - Una carpeta nueva se publica en segundos con recarga elegante (mismo PID), y las redirecciones de Apache conservan `https` y el puerto.
  - Apache 2.4.69 de Apache Lounge (14,5 MB, SHA-256 comprobado) se instala junto al de Laragon y pasa a atender todos los sitios.
  - `clon apache use` cambia de versión en unos 6 s, y los sitios responden en cuanto termina.
- **Supervisor:** un `php-cgi` que se cae se reinicia solo. Si se mata `clon.exe` de golpe, no quedan procesos huérfanos. El apagado es ordenado (Apache con su señal, MySQL con `mysqladmin`).
- **Panel:** sin token responde 403, y con otro `Host` u `Origin` no es alcanzable.
- **Laragon:** clon se niega a abrir los datos de MySQL de Laragon mientras Laragon los usa.
- **Node y atajos:** Node 24 LTS descargado y verificado; `npm`, `npx`, `php` y `composer` funcionan desde `shims\`.
- **Bases de datos:**
  - Funcionan a la vez MySQL 8.0 (de Laragon), MySQL 8.4 y MariaDB 11.8 (descargadas), cada una en su puerto.
  - Al cambiar la predeterminada, el atajo `mysql` la sigue.
  - «Iniciar todo» arranca solo las predeterminadas y las marcadas.
  - Cancelar una descarga no deja restos. Desinstalar borra los programas descargados, conserva los datos y nunca toca los archivos de Laragon.
- **Nginx:** un sitio pasa de Apache a Nginx (el 1.22 de Laragon y el 1.30 descargado) y vuelve, y PHP ve lo mismo con los dos: `HTTPS=on`, el puerto público y la URI enrutada a `index.php`. Al instalar otra versión, Nginx cambia a ella solo, y se detiene cuando ningún sitio lo usa.
- **FrankenPHP:** un sitio con FrankenPHP 1.13 responde con su PHP 8.5 propio, `HTTPS=on`, MySQL y OPcache. Tres sitios corren a la vez, uno con cada servidor (Apache, Nginx y FrankenPHP).
- **MongoDB:** la 9.0 se instala con `mongosh`, arranca como predeterminada, responde desde el atajo `mongosh` y se detiene de forma ordenada («shutdown complete», código 0).
- **PostgreSQL 18:** crea sus datos, arranca sin permisos de administrador, responde a `psql`, se detiene con `pg_ctl` y conserva las bases al reiniciar. Sin pgAdmin ocupa 156 MB.
- **Redis 8.10** (comunitario) responde a `redis-cli` y guarda sus datos al detenerse; **Mailpit** recibe un correo por SMTP; **Meilisearch 1.55** responde en `/health`. Todo a la vez que MySQL, MariaDB y MongoDB.
- **Adminer 6.1** entra sin contraseña como root en MySQL 8.0 y como postgres en PostgreSQL 18, y lista sus bases. Una petición a `db.localhost` desde otra dirección recibe 403.
- **Proyectos nuevos:**
  - `clon new "Prueba MySQL" --db mysql` deja en 3 minutos `https://prueba-mysql.test` respondiendo, con su `.env` completo y la base migrada (9 tablas).
  - `clon new prueba-react --kit react --db postgresql` tarda 5 minutos (Composer, los pasos del kit, migraciones en PostgreSQL, npm y Vite) y sirve la página de Inertia con el frontend compilado.
  - Cancelar una creación corta todo el árbol de procesos (Composer, Artisan, npm).
- **Procesos del proyecto:**
  - La cola de un proyecto con PostgreSQL procesa un job en 30 ms, y el programador corre cada minuto. Ambos se detienen y vuelven con «Detener todo» e «Iniciar todo».
  - Vite+ 0.3 sirve HTTPS con el certificado de clon, y `public/hot` desaparece al apagarlo.
  - Probado: una opción `--host` haría que la página cargara Vite desde `127.0.0.1`, que el certificado no cubre; por eso clon no la usa.
- **Xdebug por sitio:**
  - El sitio depurado carga Xdebug y los demás no.
  - Sin editor escuchando, la página responde en 0,25 s; con uno en el 9003, Xdebug se conecta y le envía el `init` de DBGp con la ruta del script.
  - Para PHP 7.2, que no tiene Xdebug publicado, clon lo explica en vez de fallar a medias.

En un Windows limpio (Windows Sandbox), con los puertos 80 y 443, pasan los 26 pasos de `probar-en-sandbox.ps1`:

- Instalación del runtime de Visual C++ y PHP descargado de windows.php.net.
- Proyecto Laravel 13 nuevo con MySQL, migraciones y `npm run build`.
- MariaDB instalada desde el catálogo, corriendo a la vez que MySQL.
- Nginx y FrankenPHP instalados desde el catálogo: Laravel servido por Nginx y otro sitio por FrankenPHP con su PHP 8.5.
- Archivo `hosts` y CA local de confianza: Edge abre los sitios sin avisos de certificado.
- Cambio de PHP en caliente, panel, atajos y cierre sin procesos sueltos.

## Hoja de ruta

- **Fase 1 (núcleo):** completa.
- **Fase 2 (versiones y servicios):** completa.
  - Sección «Servicios» en el panel; MySQL y MariaDB en varias versiones a la vez; Nginx y FrankenPHP por sitio; PostgreSQL, MongoDB, Redis, Mailpit y Meilisearch.
  - Apache de Apache Lounge en varias versiones; PHP fijado por sitio para proyectos antiguos (como PHP va por FastCGI, no hace falta un Apache viejo para un PHP viejo); extensiones de PHP con casillas; Adminer en `https://db.localhost`.
- **Fase 3:**
  - Instaladores: Laravel con sus starter kits, WordPress, Symfony, desde Git y recetas propias.
  - `.env` automático y procesos del proyecto (cola, scheduler, Reverb, Vite).
  - Vite con HTTPS, Xdebug por sitio y compartir con ngrok.
- **Fase 4:** DNS comodín para `*.sitio.test`, modo worker de FrankenPHP, actualizador y firma de código.

## Licencia

[MIT](LICENSE)
