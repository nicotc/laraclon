# Nido

**Donde viven tus proyectos.** Nido es un entorno de desarrollo local para Windows, pensado sobre todo para Laravel: PHP, bases de datos, Node y sitios `https://*.test` en un solo programa escrito en Go. Todo lo que usa lo descarga de las fuentes oficiales y lo comprueba.

**Estado:** fases 1 (núcleo) y 2 (versiones y servicios) completas; de la 3 (proyectos) solo falta compartir los sitios por Internet. Probado en Windows 11 y de punta a punta en un Windows limpio (una VM de Hyper-V).

## Qué hace hoy

- **Salud del entorno, sin abrir el navegador:** Nido la comprueba solo cada minuto. La pestaña «Salud» del panel muestra el detalle; en la bandeja, el icono lleva un punto rojo y el menú dice cuántas cosas fallan.
  - Muestra qué PHP corre (versión, `php.ini` y extensiones) y prueba de verdad cada base de datos y servicio en marcha: MySQL, MariaDB y PostgreSQL (versión y cuántas bases tiene), Redis (`PING`), Mailpit (SMTP), Meilisearch (`/health`) y MongoDB.
  - También prueba la base de datos de cada sitio, la que dice su propia configuración: el `.env` de Laravel, `DATABASE_URL` de Symfony o `wp-config.php` de WordPress. Cada fila dice OK o FALLO, con el motivo; los fallos van primero.
  - La misma comprobación está en `https://salud.localhost`, y con `?formato=json` responde para programas: HTTP 200 si todo está bien y 503 si algo falla.
- **Ficha de cada sitio:** el nombre del sitio en la pestaña «Sitios» la abre (o `#sitio/tienda` en el panel).
  - Reúne su dirección, su carpeta, el servidor web y el PHP que usa, sus procesos y su base de datos según la última comprobación de salud.
  - Edita sus archivos de configuración: el `.env`, el `.htaccess` y el `.user.ini` de la carpeta pública, y directivas propias de Apache (dentro de su `<VirtualHost>`) y de Nginx (dentro de su bloque `server`).
  - Antes de aplicar un cambio, lo revisa el programa que lo lee: Apache (`httpd -t`), Nginx (`nginx -t`) o el PHP del sitio, que avisa de los valores que no conoce y de los que no se pueden cambiar por carpeta. En el `.env`, Nido detecta los errores que rompen Laravel (un valor con espacios sin comillas, comillas sin cerrar) y Laravel lo vuelve a leer: si no puede, el cambio no queda.
  - Un `.htaccess` se prueba pidiendo la portada al sitio: si Apache lo rechaza, vuelve el anterior y se ve el motivo. Las directivas se aplican sin cortar peticiones, los procesos del sitio se reinician para leer el `.env` nuevo y la caché de configuración de Laravel se borra si la hay.
  - Cada guardado conserva la versión anterior (las últimas 10, en `state\copias`): «Deshacer el último cambio» la trae de vuelta. Si el archivo cambió fuera de Nido desde que se abrió, no se pisa.
- **`php.ini` propio y `my.ini` propio:** sección «php.ini propio» de la pestaña «PHP y Node» (uno para todas las versiones y uno por versión de PHP), y «Configuración (my.ini)» en el menú «⋯» de cada MySQL o MariaDB.
  - Tus valores ganan a los de Nido. PHP comprueba que arranca con ellos y Nido reinicia sus procesos; MySQL y MariaDB validan los ajustes sin arrancar (`--validate-config`) y, si están en marcha, se reinician con ellos. Si no arrancan, vuelve la versión anterior.
- **Proyectos Laravel nuevos en un paso:** botón «Nuevo proyecto» en la pestaña «Sitios», o `nido new tienda --kit react --db mysql`.
  - Laravel sin kit o con los kits oficiales: React, Vue, Svelte o Livewire. Se crean como con el instalador de Laravel, incluidos los pasos propios de cada kit.
  - Nido escribe el `.env` (nombre, `APP_URL` en https, la base de datos elegida y el correo a Mailpit), crea la base en MySQL, MariaDB o PostgreSQL (o usa SQLite) y migra.
  - Instala las dependencias del frontend y lo compila con Vite, con npm o, como `laravel new`, con pnpm o yarn (`--pnpm`, `--yarn`).
  - Desde el inicio se elige el entorno: el servidor web (Apache, Nginx o FrankenPHP), el PHP del sitio y dónde va la base de datos (la instancia, el nombre de la base y, si quieres, un usuario propio con contraseña generada, en vez de root). También si se siembran los datos de ejemplo. En la terminal: `--servidor nginx --php 8.3 --instancia mysql-8.0 --base tienda_db --usuario-propio --sembrar`.
  - El sitio queda en `https://tienda.test`. El panel muestra cada fase con su tiempo, las últimas líneas de lo que imprimen Composer y npm y una señal de que siguen trabajando, y se puede cancelar o dejar en segundo plano.
- **WordPress en un paso:** «Nuevo proyecto → WordPress», o `nido new blog --wordpress [--idioma es_ES]`.
  - Nido descarga el zip oficial de wordpress.org (comprobado con su SHA-1 y después archivo por archivo con `wp core verify-checksums`) y la traducción elegida.
  - Crea la base de datos en MySQL o MariaDB, escribe `wp-config.php` e instala el sitio en ese idioma, con el contenido de ejemplo, el tema y los plugins traducidos, y enlaces permanentes (`/%postname%/`).
  - El usuario es `admin` con una contraseña generada, que se muestra al terminar. El sitio queda en `https://blog.test`.
  - WP-CLI se descarga de GitHub (con su SHA-256) y queda como atajo `wp`.
- **Symfony:** «Nuevo proyecto → Symfony», o `nido new api --symfony [--webapp] [--db mysql]`.
  - `composer create-project symfony/skeleton` (Composer elige la versión que admite tu PHP) y, con «webapp», Twig, Doctrine, formularios, seguridad y correo.
  - Para Apache añade `symfony/apache-pack`, cuyo `.htaccess` manda las rutas a `public/index.php`, y no agrega archivos de Docker.
  - Escribe `.env.local` con la dirección del sitio (`DEFAULT_URI`), la base de datos elegida (`DATABASE_URL`, que Nido crea) y Mailpit (`MAILER_DSN`).
- **Plantillas propias (recetas):** «Nuevo proyecto → Plantilla», o `nido new panel --receta laravel-filament`.
  - Cada receta es un archivo `.toml` en `config\recetas`: de dónde sale el proyecto (Laravel con su kit, WordPress, Symfony, Git, un paquete de Composer o un proyecto guardado), su base de datos, los pasos que lo terminan (por ejemplo `composer require filament/filament`) y valores para el `.env`.
  - Los pasos corren en la carpeta del proyecto con el `php`, `composer` y `npm` de Nido. La primera vez Nido deja una receta de ejemplo con Filament.
  - «⧉» junto a un sitio (o `nido site template tienda`) lo guarda como plantilla, sin `vendor`, `node_modules`, `.env`, la base SQLite ni lo que Laravel deja en `storage`. Los proyectos que salen de ella se preparan como uno recién clonado.
- **Proyectos existentes y desde Git:**
  - Un proyecto copiado o clonado al que le falta algo muestra «Preparar» en la pestaña «Sitios» (o `nido site setup tienda`).
  - Nido instala los paquetes de Composer con el PHP del sitio y crea el `.env` desde `.env.example`, con la base de datos que pide el proyecto apuntando a las de Nido.
  - También genera `APP_KEY`, crea la base y la migra solo si es nueva (los datos existentes no se tocan), enlaza `public/storage` y compila el frontend.
  - «Nuevo proyecto → Desde Git» (o `nido new tienda --git <url> [--rama <rama>]`) clona el repositorio y lo prepara igual. Si el equipo no tiene Git, Nido descarga MinGit, el Git portátil oficial de Git for Windows (con su SHA-256).
- **Procesos de cada proyecto:** columna «Procesos» de la pestaña «Sitios», o `nido site proc tienda vite on`.
  - Vite (`npm run dev`), la cola (`queue:work`) y las tareas programadas (`schedule:work`) corren supervisados, con su registro en `logs\`. Los que dejas encendidos arrancan con «Iniciar todo».
  - Vite va por HTTPS con el certificado del sitio, en el que Windows ya confía: la página y la recarga en caliente usan `https://tienda.test:5173`, sin tocar `vite.config`.
  - Nido escribe también `VITE_DEV_SERVER_KEY` y `VITE_DEV_SERVER_CERT` en el `.env`, así `npm run dev` en una terminal también sirve HTTPS. Al apagar Vite, Nido borra `public/hot`.
  - Vite necesita que el sitio esté en el archivo `hosts`; si falta, Nido lo avisa y lo arranca al actualizar `hosts`.
  - La cola y las tareas usan el PHP del sitio (el fijado o el global) y se reinician al cambiarlo.
  - Reverb, los WebSockets de Laravel, en los proyectos que lo usan (`php artisan install:broadcasting --reverb`): corre en un puerto interno y el proxy lo publica en `wss://reverb.tienda.test` con el certificado del sitio.
  - Al encenderlo, Nido apunta `REVERB_HOST`, `REVERB_PORT` y `REVERB_SCHEME` del `.env` a esa dirección, así Echo en el navegador y `broadcast()` en PHP llegan por el mismo camino. Si el frontend ya estaba compilado con otra dirección, lo vuelve a compilar.
- **Xdebug por sitio:** botón «Xdebug» junto al PHP del sitio, o `nido site debug tienda on`.
  - Ese sitio pasa a un grupo de `php-cgi` de su versión con Xdebug, que se conecta al editor (VS Code, PhpStorm) en 127.0.0.1:9003. Los demás siguen igual de rápidos, sin Xdebug.
  - Si a esa versión le falta Xdebug, Nido lo descarga.
  - `phpx` es el PHP global con Xdebug, para depurar Artisan y los tests desde la terminal; `php` sigue sin él.
- **Sitios `.test` automáticos con HTTPS.**
  - Cada carpeta de tus raíces de proyectos se publica como `https://<carpeta>.test`.
  - Las carpetas nuevas aparecen solas, sin cortar Apache.
  - Los nombres raros se normalizan: `mi proyecto 2` → `mi-proyecto-2.test`.
- **Proxy frontal propio (80/443).** Presenta un certificado por sitio, firmado por una CA local, y redirige HTTP a HTTPS. Los sitios desconocidos reciben una página de error útil.
- **PHP global con cambio en caliente.** Funciona con cualquier versión, incluida la 7.2. PHP corre por FastCGI (un pool de `php-cgi`), así que Apache no se reinicia al cambiarlo, y la terminal, Composer y Artisan siguen al mismo PHP.
  - Excepción para proyectos antiguos: un sitio puede fijar su propia versión en la pestaña «Sitios» o con `nido site php tienda 7.2`.
  - Ese sitio corre en un grupo de `php-cgi` propio, con Apache o Nginx, y sigue igual aunque cambies el PHP global.
  - Dentro de su carpeta, `php`, `composer` y `artisan` usan esa versión, también en un proyecto recién creado con su PHP elegido. Si es anterior a 7.2.5, Nido usa Composer 2.2 LTS, el último que la admite.
  - PHP 5.x necesita además el runtime de Visual C++ de su época (2012 para PHP 5.5 y 5.6).
- **Extensiones de PHP con casillas:** pestaña «PHP y Node» o `nido php ext on soap`, por versión de PHP.
  - Xdebug y MongoDB se descargan de sus repositorios oficiales: las compilaciones para Windows que publican para PIE, con su SHA-256.
  - Se guardan en una carpeta propia de Nido, fuera de la carpeta de cada PHP.
  - Antes de aplicar un cambio, Nido comprueba que PHP cargue la extensión; si no carga (por ejemplo, le falta una DLL), no cambia nada y muestra el aviso de PHP.
  - Los procesos de esa versión se reinician solos.
  - phpredis y APCu no publican compilaciones para Windows; Laravel funciona igual con `predis` y la caché en archivos.
- **Apache 2.4** con un puerto interno por sitio y `.htaccess` (`AllowOverride All`). PHP recibe `HTTPS=on` y las URLs correctas.
  - Se descarga de Apache Lounge (la compilación de referencia para Windows) con su SHA-256 comprobado. Apache Lounge mantiene esas compilaciones de forma voluntaria y acepta donaciones en apachelounge.com.
  - Puedes tener varias versiones: el panel (o `nido apache use 2.4.62`) cambia en caliente la que atiende los sitios.
- **Nginx o FrankenPHP por sitio.** Cada sitio elige Apache, Nginx o FrankenPHP en la pestaña «Sitios» (o con `nido site server tienda nginx`), sin cambiar su dirección `.test`.
  - Ambos se instalan desde el catálogo: los repositorios oficiales github.com/nginx/nginx y github.com/php/frankenphp, con el SHA-256 comprobado.
  - Solo arrancan cuando algún sitio los elige, y se recargan en caliente.
  - Nginx usa el PHP global; FrankenPHP usa su propio PHP (8.5 en la 1.13), con un `php.ini` que genera Nido.
- **MySQL y MariaDB, varias versiones a la vez.** La pestaña «Servicios» del panel (o `nido service`) instala versiones desde un catálogo de descargas oficiales:
  - MySQL de cdn.mysql.com, con la firma de Oracle comprobada.
  - MariaDB de mariadb.org, con su SHA-256 comprobado.

  Cada versión tiene su puerto, su carpeta de datos y su interruptor. Una por tipo es la predeterminada: usa el 3306, arranca con «Iniciar todo» y es la que abre el atajo `mysql`.
- **PostgreSQL, MongoDB, Redis, Mailpit y Meilisearch.** Se instalan desde el mismo catálogo, cada versión con su puerto, sus datos y su interruptor; la predeterminada usa el puerto estándar (5432, 27017, 6379, 1025 y 7700).
  - **PostgreSQL:** zip oficial de get.enterprisedb.com, enlazado desde postgresql.org, sin pgAdmin. EnterpriseDB no publica sumas ni firma esos programas, así que Nido comprueba que el zip esté íntegro y que traiga la versión esperada. El servidor corre sin permisos de administrador (PostgreSQL se niega a usarlos) y se detiene con `pg_ctl`. Atajo `psql`.
  - **MongoDB:** desde mongodb.org con su SHA-256, junto con `mongosh`, que sirve para detenerlo de forma ordenada y queda como atajo.
  - **Redis:** Redis no publica versión para Windows; Nido usa la compilación comunitaria github.com/redis-windows (con su SHA-256). Atajo `redis-cli`.
  - **Mailpit:** recibe lo que envían `mail()` de PHP y Laravel (SMTP en el 1025) y lo muestra en su página (8025).
  - **Meilisearch:** para Laravel Scout, en modo desarrollo y con su panel en el navegador.
  - Lo primero que se instala de cada tipo pasa a ser su predeterminado.
  - «Copiar datos de conexión» da las líneas del `.env` de Laravel para cada tipo.
- **Adminer en `https://db.localhost`** para ver y editar las bases de datos en el navegador (MySQL, MariaDB, PostgreSQL y SQLite).
  - Se instala desde el catálogo (github.com/vrana/adminer, con su SHA-256) y corre con el PHP global.
  - Entra sin contraseña con root o postgres, y solo responde a este equipo, aunque el proxy se abra a la red local.
  - Cada base de datos del panel tiene «Abrir en Adminer» en su menú.
- **Node incluido y al día:** versiones oficiales de nodejs.org, con su SHA-256 comprobado, cada una con `npm` y `npx`.
  - La pestaña «PHP y Node» (o `nido node`) instala, cambia y borra versiones. Al cambiar, los servidores de Vite se reinician con la nueva, y los paquetes globales de npm se conservan.
  - Avisa cuando sale una LTS nueva o una versión que corrige fallos de seguridad de la tuya, con un botón para actualizar.
  - **Node por sitio:** un proyecto puede fijar su versión en su ficha, al crearlo o con `nido site node tienda 18`. Su Vite, la compilación de su frontend y, dentro de su carpeta, `node`, `npm`, `npx`, `pnpm`, `yarn` y los scripts de npm usan esa versión; los demás siguen con la global.
  - Si el proyecto pide una versión en `.nvmrc` o `.node-version` y no es la que usa, su ficha la ofrece con un botón (y la instala si falta).
- **pnpm y yarn** se instalan con un botón (o `nido node install pnpm`), como paquetes globales de npm, con sus atajos.
  - Cada proyecto usa su gestor, según su lockfile o el campo `packageManager`: Vite, «Preparar» y «Nuevo proyecto» lo respetan y lo instalan si falta.
  - Los proyectos de Yarn 2 o posterior van por Corepack, que Nido instala aparte cuando el Node ya no lo trae (desde Node 25).
- **Composer** actualizado y atajos de terminal (`php`, `php84`, `php83`…, `composer`, `wp`, `node`, `npm`, `npx`, `pnpm`, `yarn`, `mysql`) en una sola carpeta `shims\`. Los procesos PHP que lanzan Composer o Artisan heredan el mismo `php.ini`.
- **Supervisor.** Los servicios corren ocultos dentro de *job objects*: si Nido muere, Windows los cierra todos. Si uno se cae, se reinicia, y el apagado normal es ordenado.
- **Bandeja, panel web y línea de comandos.** El panel se abre en `https://localhost` (o en la dirección local de control mientras la CA no es de confianza) y su API está protegida con token, verificación de `Host` y de `Origin`.

## Requisitos

- **Windows 10 u 11 de 64 bits.**
- **Runtime de Visual C++ x64 de Microsoft** (v14, el de Visual Studio 2015 a 2026). Lo necesitan los binarios de PHP, Apache y MySQL para Windows.
  - Si falta, `nido setup` (o `nido vcredist install`, o el botón del panel) instala la versión oficial más reciente tras comprobar su firma de Microsoft. Windows pide permiso de administrador. En un Windows recién instalado, el instalador de Microsoft tarda unos 4 minutos.
  - El Apache actual de Apache Lounge se compila con Visual Studio 2026, y Microsoft pide para él el runtime 14.50 o posterior. Funciona igual con los anteriores (probado con 14.44 y 14.32), así que Nido no bloquea nada: solo avisa y ofrece actualizarlo. Si otros programas tienen abierto el runtime, Windows termina de actualizarlo al reiniciar, y el aviso lo indica.
- **Go 1.27 o posterior,** solo para compilar.
- **Nada más:** Nido descarga solo PHP, Apache, MySQL, Node, Composer y Git, de sus fuentes oficiales y comprobados. `nido setup` deja listos la última versión de PHP y de Apache, MySQL 8.4 LTS, Node LTS y Composer. Git (MinGit) se descarga la primera vez que clonas un proyecto, si el equipo no tiene.
- **Otras bases de datos y servicios** (MariaDB, PostgreSQL, MongoDB, Redis, Mailpit, Meilisearch): se instalan desde el panel o con `nido service install`.

## Compilar

```powershell
go test ./...
go build -o C:\nido\nido.exe ./cmd/nido
go build -o C:\nido\nido-shim.exe ./cmd/nido-shim
go build -ldflags "-H=windowsgui" -o C:\nido\nidow.exe ./cmd/nidow
```

Los tres ejecutables van juntos en la carpeta de instalación, que no debe tener espacios en la ruta:
- `nido.exe`: la línea de comandos.
- `nidow.exe`: el programa con la bandeja.
- `nido-shim.exe`: la plantilla de los atajos.

Todo lo que Nido descarga o genera queda dentro de esa carpeta. Para usar otra carpeta, define `NIDO_HOME`.

## Primer uso

```powershell
C:\nido\nido.exe setup     # descarga PHP, Apache, MySQL 8.4 LTS, Node LTS (con npm y npx), Composer y la lista de CAs públicas
C:\nido\nido.exe run       # arranca Nido con el icono en la bandeja
C:\nido\nido.exe status    # servicios, sitios y avisos
```

Tus proyectos van en `C:\nido\www`: cada carpeta es un sitio, `C:\nido\www\tienda` es `https://tienda.test`. Puedes sumar otras carpetas de proyectos en `roots` de `C:\nido\config\nido.toml`.

Si otro programa ya usa los puertos 80, 443 o 3306 (otro entorno local, IIS…), elige otros puertos para el proxy en `nido.toml`, y para las bases de datos en el panel o con `nido service port mysql-8.4 3307`:

```toml
[proxy]
  http_port = 8081
  https_port = 8444
```

## Dejarlo listo en tu equipo

1. `nido setup`: descarga todo lo necesario (unos minutos la primera vez).
2. `nido ca trust`: Windows te pide confirmar la CA local, solo para tu usuario. Así los sitios abren por HTTPS sin avisos.
3. `nido hosts sync`: Windows pide permiso de administrador para escribir el archivo `hosts`.
4. `nido path add` (opcional): `php`, `composer`, `node` y `npm` de Nido en cualquier terminal.
5. `nido run` y abre `https://salud.localhost`: si todo está en verde, PHP, las bases de datos y tus sitios responden.

## Comandos

| Comando | Qué hace |
|---|---|
| `nido run [--foreground]` | Inicia Nido (bandeja), o en la consola actual con `--foreground` |
| `nido new tienda [--kit react\|vue\|svelte\|livewire] [--db sqlite\|mysql\|mariadb\|postgresql] [--pnpm\|--yarn]` | Proyecto Laravel nuevo en `https://tienda.test` |
| `nido new blog --wordpress [--idioma es_ES] [--db mysql\|mariadb]` | WordPress en `https://blog.test`, con usuario admin y contraseña generada |
| `nido new api --symfony [--webapp] [--db sqlite\|mysql\|mariadb\|postgresql]` | Symfony en `https://api.test`, con su `.env.local` |
| `nido new tienda --git <url> [--rama <rama>]` · `nido site setup tienda` | Clona y prepara un proyecto · prepara uno que ya está en la carpeta |
| `nido new panel --receta laravel-filament` · `nido recipe list` · `nido site template tienda [nombre]` | Proyecto desde una plantilla propia · las plantillas · guardar un sitio como plantilla |
| `nido start` · `stop` · `restart` · `quit` | Servicios on/off; `quit` cierra Nido |
| `nido status` · `sites` · `versions` | Estado, sitios y versiones instaladas |
| `nido php list` · `remote` · `install 8.4` · `use 8.3` | Versiones de PHP |
| `nido php ext [8.4]` · `ext on xdebug` · `ext off exif [8.3]` | Extensiones de cada versión de PHP |
| `nido apache list` · `remote` · `install` · `use 2.4.62` | Versiones de Apache (descarga de Apache Lounge) |
| `nido node list` · `remote` · `install lts [--use]` · `use 24` · `uninstall 22.11.0` | Versiones de Node (con npm y npx) y sus avisos |
| `nido node install pnpm\|yarn` · `uninstall pnpm` | pnpm y yarn como paquetes globales, con sus atajos |
| `nido composer install` | Descarga o actualiza Composer |
| `nido vcredist status` · `install [instalador.exe]` | Runtime de Visual C++ que necesitan PHP, Apache y MySQL |
| `nido service list` · `remote` · `install postgresql latest` | Servicios instalados · catálogo · instalar (mysql, mariadb, postgresql, mongodb, redis, mailpit, meilisearch) |
| `nido service start` · `stop` · `default` · `uninstall mysql-8.4` | Una instancia: iniciar, detener, hacerla predeterminada o quitarla (los datos se conservan) |
| `nido service port mysql-8.4 3307` · `autostart mysql-8.4 on` | Su puerto y si arranca con «Iniciar todo» |
| `nido site server tienda nginx` · `frankenphp` · `apache` | Servidor web de un sitio |
| `nido site php tienda 7.2` · `global` | PHP propio para un proyecto antiguo, o vuelta al global |
| `nido site proc tienda vite\|queue\|schedule\|reverb on\|off` | Procesos del proyecto: Vite con HTTPS, cola, tareas programadas y Reverb |
| `nido site debug tienda on\|off` · `phpx` | Xdebug solo para ese sitio · PHP con Xdebug en la terminal |
| `nido service install nginx latest` · `frankenphp 1.13` | Instala un servidor web desde el catálogo |
| `nido service install adminer latest` | Adminer en `https://db.localhost` |
| `nido ca status` · `trust` · `untrust` | CA local para HTTPS |
| `nido hosts status` · `sync` | Bloque gestionado del archivo `hosts` |
| `nido path add` · `remove` | Atajos de Nido en el PATH de tu usuario |

`NIDO_APACHE_LOGLEVEL="warn proxy_fcgi:trace8"` hace que Apache registre las variables que envía a PHP (útil para diagnosticar).

## Probar en un Windows limpio (VM de Hyper-V)

```powershell
# Una vez: crea la VM (Windows pide permiso de administrador; después hay que cerrar sesión y volver a entrar)
.\scripts\vm\probar-en-vm.ps1 -Crear -Iso <ISO de Windows 11> -VmRoot D:\VMs
# Cada prueba (con -DejarEncendida, la VM queda encendida al terminar para mirarla)
.\scripts\vm\probar-en-vm.ps1 -VmRoot D:\VMs
# Cambiar la memoria máxima de la VM o la contraseña de su cuenta Nido
.\scripts\vm\ajustar-vm.ps1 -MemoryGB 6 -Password <nueva> -VmRoot D:\VMs
```

- **La VM se crea sin asistente:**
  - `crear-vm.ps1` aplica Windows 11 Pro del ISO directamente a un disco virtual dinámico. No hace falta activarlo para probar.
  - Crea una cuenta `nido` de administrador que entra sola y guarda un checkpoint «limpia» con la sesión ya iniciada.
  - Tu usuario pasa al grupo «Administradores de Hyper-V», así las pruebas no vuelven a pedir permiso.
  - Memoria dinámica: arranca con 2 GB, baja a 1 GB en reposo y sube hasta 6 GB (`-MemoryGB`) solo mientras la prueba lo necesita.
  - MAC fija: con una dinámica, al volver al checkpoint chocaba con la que el «Default Switch» había tomado entretanto, y la VM se quedaba minutos sin red.
- **Cada prueba:**
  - Compila Nido, vuelve la VM a «limpia», le copia Nido por PowerShell Direct (por Hyper-V, sin red) y ejecuta `dentro-de-la-vm.ps1` en la sesión de Windows. Antes comprueba que la VM tiene Internet.
  - Recorre todo el flujo real:
    - Runtime de Visual C++ y `nido setup`: PHP, Apache, MySQL, Node y Composer descargados, sin nada instalado antes.
    - PHP 8.3 y 7.2 descargados de windows.php.net.
    - Confiar en la CA y escribir el archivo `hosts`.
    - Puertos 80/443.
    - Proyecto Laravel nuevo con MySQL y `npm run build`.
    - MariaDB instalada desde el catálogo y corriendo a la vez que MySQL.
    - PHP 7.2 fijado en un sitio, en la web y en la terminal, mientras el resto usa el global.
    - Extensiones de PHP: Xdebug descargado y soap, activadas y apagadas.
    - Adminer en `https://db.localhost`, con entrada sin contraseña en MySQL.
    - Proyecto nuevo con `nido new` (kit de Vue, MySQL y pnpm), otro clonado de GitHub y preparado, y los procesos de un tercero: Vite por HTTPS, cola y tareas.
    - Node: otra versión instalada y borrada sin tocar la global, y yarn y pnpm desde los atajos.
    - Xdebug solo en un sitio, y `phpx` en la terminal.
    - Reverb en `wss://reverb.laravel-demo.test`: un cliente WebSocket de Node recibe el evento que envía PHP, los dos a través del proxy.
    - WordPress con `nido new --wordpress`: en español, con enlaces permanentes y el acceso de admin.
    - Symfony con `nido new --symfony --webapp`: el router responde a través del `.htaccess` y Doctrine usa MySQL.
    - Una plantilla propia guardada desde un proyecto y otro proyecto creado a partir de ella.
    - Apache de Apache Lounge, cambio de versión en caliente y actualización de Visual C++ con Nido en marcha.
    - La página de salud: todo OK en `https://salud.localhost` y en su JSON.
    - Edge sin avisos, cambio de PHP y cierre.
  - Tarda alrededor de una hora y muestra cada paso a medida que termina.
  - El informe queda en `dist\vm\<fecha>\resultados\report.md`, y el avance, con la memoria que llegó a usar la VM, en `dist\vm\<fecha>\progreso.log`.
  - Al terminar apaga la VM, para que su memoria vuelva al equipo. Con `-DejarEncendida` sigue encendida con Nido arrancado y el proyecto Laravel y el panel abiertos en Edge, para probarlo a mano (`vmconnect.exe localhost nido-pruebas`).
- Los instaladores de Visual C++ se descargan en el equipo anfitrión (`dist\cache`) y se copian a la VM. Primero se instala a propósito el de Visual Studio 2022 (14.44) y después el más reciente.
- Antes se usaba Windows Sandbox, pero se cerraba solo a mitad de la prueba (error 0x80370106) y al cerrar su ventana se perdía todo.

## Estructura

```text
cmd/nido         línea de comandos            internal/proxy       proxy frontal HTTPS
cmd/nidow        bandeja (sin consola)        internal/ca          CA local y certificados
cmd/nido-shim    atajos php/node/npm…         internal/sites       detección de sitios y puertos
internal/app     servicio principal y API     internal/hosts       bloque del archivo hosts
internal/apache  httpd.conf y control         internal/supervisor  procesos y job objects
internal/php     php.ini y pool de php-cgi    internal/ports       puertos libres y reservados
internal/mysql   MySQL y MariaDB: my.ini      internal/install     descargas oficiales verificadas
internal/nginx   nginx.conf y control         internal/frankenphp  Caddyfile y control
internal/mongodb mongod y parada con mongosh  internal/postgres    initdb, servidor sin admin
internal/redis   redis.conf y redis-cli       internal/mailpit, internal/meilisearch
internal/app/salud  página de salud (PHP)  internal/versions    catálogo de versiones
```

## Probado (octubre de 2026)

En Windows 11, en puertos alternativos (junto a otro entorno local):

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
  - Apache 2.4.69 de Apache Lounge (14,5 MB, SHA-256 comprobado) se instala junto a otro Apache y pasa a atender todos los sitios.
  - `nido apache use` cambia de versión en unos 6 s, y los sitios responden en cuanto termina.
- **Supervisor:** un `php-cgi` que se cae se reinicia solo. Si se mata `nido.exe` de golpe, no quedan procesos huérfanos. El apagado es ordenado (Apache con su señal, MySQL con `mysqladmin`).
- **Panel:** sin token responde 403, y con otro `Host` u `Origin` no es alcanzable.
- **Datos compartidos:** Nido se niega a abrir una carpeta de datos de MySQL que otro servidor tiene abierta.
- **Node y atajos:** Node 24 LTS descargado y verificado; `npm`, `npx`, `php` y `composer` funcionan desde `shims\`.
  - Con un Node 24.14.1 como global, Nido avisa de que la 24.22.0 corrige fallos de seguridad, y «Usar v24.22.0» en el aviso lo cambia.
  - Node 22 se instala en 11 s sin cambiar el global y se borra con su carpeta; el global no se deja borrar.
  - pnpm 12 (que ya trae su propio `pnpm.exe`) y yarn 1.22 se instalan con npm y responden desde `shims\` (`pnpm`, `pn`, `pnpx`, `pnx`, `yarn`, `yarnpkg`).
  - `nido new demo-pnpm --pnpm` deja el proyecto con `pnpm-lock.yaml`, sin `package-lock.json` y con los scripts de Composer apuntando a pnpm, en menos de 3 minutos.
  - Un proyecto con `"packageManager": "yarn@4.9.2"` se prepara con Yarn 4 por Corepack: con Node 24 usa el Corepack que trae, y con Node 26 Nido lo instala aparte (11 s en total).
- **Bases de datos:**
  - Funcionan a la vez MySQL 8.0, MySQL 8.4 y MariaDB 11.8, cada una en su puerto.
  - Al cambiar la predeterminada, el atajo `mysql` la sigue.
  - «Iniciar todo» arranca solo las predeterminadas y las marcadas.
  - Cancelar una descarga no deja restos. Desinstalar borra los programas descargados, conserva los datos y nunca toca programas que Nido no descargó.
- **Nginx:** un sitio pasa de Apache a Nginx (el 1.22 y el 1.30) y vuelve, y PHP ve lo mismo con los dos: `HTTPS=on`, el puerto público y la URI enrutada a `index.php`. Al instalar otra versión, Nginx cambia a ella solo, y se detiene cuando ningún sitio lo usa.
- **FrankenPHP:** un sitio con FrankenPHP 1.13 responde con su PHP 8.5 propio, `HTTPS=on`, MySQL y OPcache. Tres sitios corren a la vez, uno con cada servidor (Apache, Nginx y FrankenPHP).
- **MongoDB:** la 9.0 se instala con `mongosh`, arranca como predeterminada, responde desde el atajo `mongosh` y se detiene de forma ordenada («shutdown complete», código 0).
- **PostgreSQL 18:** crea sus datos, arranca sin permisos de administrador, responde a `psql`, se detiene con `pg_ctl` y conserva las bases al reiniciar. Sin pgAdmin ocupa 156 MB.
- **Redis 8.10** (comunitario) responde a `redis-cli` y guarda sus datos al detenerse; **Mailpit** recibe un correo por SMTP; **Meilisearch 1.55** responde en `/health`. Todo a la vez que MySQL, MariaDB y MongoDB.
- **Adminer 6.1** entra sin contraseña como root en MySQL 8.0 y como postgres en PostgreSQL 18, y lista sus bases. Una petición a `db.localhost` desde otra dirección recibe 403.
- **Proyectos nuevos:**
  - `nido new "Prueba MySQL" --db mysql` deja en 3 minutos `https://prueba-mysql.test` respondiendo, con su `.env` completo y la base migrada (9 tablas).
  - `nido new prueba-react --kit react --db postgresql` tarda 5 minutos (Composer, los pasos del kit, migraciones en PostgreSQL, npm y Vite) y sirve la página de Inertia con el frontend compilado.
  - `nido new demo-git --git https://github.com/laravel/laravel.git --db mysql` clona, instala Composer, crea el `.env` hacia MySQL, la clave y la base (9 tablas), enlaza `storage` y compila el frontend, en algo más de 3 minutos.
  - «Preparar» sobre un proyecto al que solo le faltaba el frontend lo deja listo en 48 s.
  - `nido new "Blog de Prueba" --wordpress` instala WordPress 7.1 en 53 s: portada y escritorio en español, «¡Hola, mundo!» en `/hola-mundo/` y el usuario admin entra con la contraseña generada.
  - `nido new demo-symfony --symfony --webapp --db mysql` deja Symfony 8.1 en 2,5 minutos: la página de bienvenida, una ruta inexistente respondida por el router de Symfony con la URL `https` correcta (el `.htaccess` reescribe) y Doctrine conectado a su base en MySQL.
  - Un sitio guardado como plantilla da proyectos nuevos en 2 minutos y medio: copia sin `vendor` ni datos, Composer, `.env` desde `.env.example`, clave nueva, base SQLite nueva migrada y el frontend con el pnpm de la plantilla.
  - Probado: wordpress.org ya no publica cada versión empaquetada en cada idioma, y en Windows el `.tar.gz` que baja WP-CLI no se puede extraer (PharData falla con un nombre largo de WordPress 7.1 y su plan B, `tar`, arranca sin variables de entorno). Por eso Nido baja el zip y la traducción por su cuenta.
  - Cancelar una creación corta todo el árbol de procesos (Composer, Artisan, npm).
- **Procesos del proyecto:**
  - La cola de un proyecto con PostgreSQL procesa un job en 30 ms, y el programador corre cada minuto. Ambos se detienen y vuelven con «Detener todo» e «Iniciar todo».
  - Vite+ 0.3 sirve HTTPS con el certificado de Nido, y `public/hot` desaparece al apagarlo.
  - Probado: una opción `--host` haría que la página cargara Vite desde `127.0.0.1`, que el certificado no cubre; por eso Nido no la usa.
  - Reverb, instalado en un proyecto con `php artisan install:broadcasting --reverb`, acepta conexiones en menos de 1 s desde que se enciende. Un cliente WebSocket que entra por el proxy (con el certificado de Nido verificado y `101 Switching Protocols`) se suscribe a un canal y recibe el evento que envía PHP con `Broadcast`. Al encenderlo, Nido recompila el frontend con la dirección nueva en 2 s.
- **Página de salud:** con 9 servicios y 48 sitios contesta en 1,4 s: prueba cada servicio en marcha y la base de datos de cada sitio, y encuentra los dos cuyo `.env` apunta a un usuario de MySQL que no existe («Access denied»), por lo que el JSON responde 503.
- **Proyecto nuevo con todo elegido:** `nido new entorno-demo --servidor nginx --php 8.3 --instancia mysql-8.0 --base demo_entorno --usuario-propio --sembrar` deja el sitio servido por Nginx con PHP 8.3.12, conectado a MySQL 8.0 con su propio usuario `demo_entorno` y el usuario de ejemplo sembrado.
- **Archivos de configuración** (con Apache, Nginx, PHP 8.3 y 8.4, MySQL 5.7 y MariaDB 11.8 de verdad):
  - Se rechazan, sin tocar el archivo: un `.env` con un valor con espacios sin comillas, una directiva inventada en el `.htaccess` (con el mensaje de Apache), `extension=` o una línea rota en un `.user.ini`, directivas inventadas para Apache o Nginx y una variable inexistente en el `my.ini` de MySQL 5.7 y de MariaDB.
  - Se aplican al guardar y vuelven atrás con «Deshacer»: el nombre del `.env` en el título de la página, una cabecera desde el `.htaccess` y desde las directivas de Apache y de Nginx, `memory_limit` desde el `.user.ini` (300M, y 512M al deshacer), `max_input_time` desde el `php.ini` de PHP 8.3, y `max_connections` de MariaDB, que se reinicia con 321 y vuelve a 151.
  - Probado: FrankenPHP no lee `.user.ini` (Apache y Nginx sí, con `php-cgi`), y `nginx -s reload` vuelve antes de que el proceso nuevo atienda, por eso Nido espera a ver la configuración nueva antes de dar el cambio por aplicado.
- **Node por sitio:** con Node 22 fijado en un sitio, dentro de su carpeta `node -v` y `npm exec -- node -v` dan v22.11.0 y `php` su 8.3.12; en otra carpeta, la v24.22.0 y el PHP global. Un `.nvmrc` con «22» hace que la ficha ofrezca «Usar Node 22.11.0».
- **Xdebug por sitio:**
  - El sitio depurado carga Xdebug y los demás no.
  - Sin editor escuchando, la página responde en 0,25 s; con uno en el 9003, Xdebug se conecta y le envía el `init` de DBGp con la ruta del script.
  - Para PHP 7.2, que no tiene Xdebug publicado, Nido lo explica en vez de fallar a medias.

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
- **Fase 3 (en marcha):**
  - Hecho: proyectos Laravel nuevos con sus starter kits, WordPress, Symfony, desde Git y desde plantillas propias, «Preparar» para proyectos existentes, `.env` automático, procesos del proyecto (Vite con HTTPS, cola, tareas y Reverb), Xdebug por sitio, y Node al día con pnpm y yarn.
  - Falta: compartir con ngrok.
  - Nombre e identidad propios (Nido), salud del entorno, progreso visible de los trabajos largos, proyecto nuevo con su entorno elegido, ficha de cada sitio con sus archivos de configuración y Node por sitio.
- **Fase 4:** DNS comodín para `*.sitio.test`, modo worker de FrankenPHP, actualizador y firma de código.

## Licencia

[MIT](LICENSE)
