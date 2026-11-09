<?php
// Nido: dump() y dd() también van a la pestaña «Dumps» del panel. Archivo generado: no lo edites,
// se reescribe al cambiar los ajustes. Lo carga auto_prepend_file del php.ini que escribe Nido.
// Solo actúa en proyectos con symfony/var-dumper (Laravel, Symfony), cuyas funciones dump() y dd()
// se declaran «si no existen»: un proyecto antiguo con su propio dump() sigue igual.
if (!function_exists('__nido_dump_send')) {
    define('NIDO_DUMPS_PORT', __PORT__);
    define('NIDO_DUMPS_PAGE', __PAGE__);
    $GLOBALS['__nido_dump_req'] = function_exists('random_bytes') ? bin2hex(random_bytes(6)) : uniqid('', true);

    function __nido_dump_cli()
    {
        return in_array(PHP_SAPI, array('cli', 'phpdbg', 'embed'), true);
    }

    // __nido_dump_wanted busca vendor/symfony/var-dumper subiendo desde el script (o la carpeta actual).
    function __nido_dump_wanted()
    {
        $dir = __nido_dump_cli() ? getcwd() : (isset($_SERVER['SCRIPT_FILENAME']) ? dirname($_SERVER['SCRIPT_FILENAME']) : '');
        for ($i = 0; $i < 6 && $dir !== '' && $dir !== false; $i++) {
            if (is_dir($dir . DIRECTORY_SEPARATOR . 'vendor' . DIRECTORY_SEPARATOR . 'symfony' . DIRECTORY_SEPARATOR . 'var-dumper')) {
                return true;
            }
            $up = dirname($dir);
            if ($up === $dir) {
                break;
            }
            $dir = $up;
        }
        return false;
    }

    function __nido_dump_text($value)
    {
        if (class_exists('Symfony\Component\VarDumper\Cloner\VarCloner') && class_exists('Symfony\Component\VarDumper\Dumper\CliDumper')) {
            try {
                $cloner = new \Symfony\Component\VarDumper\Cloner\VarCloner();
                $dumper = new \Symfony\Component\VarDumper\Dumper\CliDumper();
                $dumper->setColors(false);
                return rtrim((string) $dumper->dump($cloner->cloneVar($value), true));
            } catch (\Throwable $e) {
            } catch (\Exception $e) {
            }
        }
        if (is_string($value)) {
            return '"' . $value . '"';
        }
        if (is_bool($value)) {
            return $value ? 'true' : 'false';
        }
        if ($value === null) {
            return 'null';
        }
        return print_r($value, true);
    }

    // __nido_dump_where is the first place outside vendor\ that called dump() (the project's line).
    function __nido_dump_where()
    {
        $first = null;
        foreach (debug_backtrace(DEBUG_BACKTRACE_IGNORE_ARGS, 12) as $f) {
            if (!isset($f['file']) || $f['file'] === __FILE__) {
                continue;
            }
            if ($first === null) {
                $first = $f;
            }
            if (strpos(str_replace('\\', '/', $f['file']), '/vendor/') === false) {
                return $f;
            }
        }
        return $first === null ? array('file' => '', 'line' => 0) : $first;
    }

    function __nido_dump_send($value, $kind)
    {
        $cli = __nido_dump_cli();
        $where = __nido_dump_where();
        $msg = array(
            'request' => $GLOBALS['__nido_dump_req'],
            'kind' => $kind,
            'text' => __nido_dump_text($value),
            'type' => is_object($value) ? get_class($value) : gettype($value),
            'file' => isset($where['file']) ? $where['file'] : '',
            'line' => isset($where['line']) ? (int) $where['line'] : 0,
            'host' => $cli ? '' : (isset($_SERVER['HTTP_HOST']) ? $_SERVER['HTTP_HOST'] : ''),
            'method' => $cli ? 'CLI' : (isset($_SERVER['REQUEST_METHOD']) ? $_SERVER['REQUEST_METHOD'] : ''),
            'uri' => $cli ? implode(' ', isset($_SERVER['argv']) ? (array) $_SERVER['argv'] : array()) : (isset($_SERVER['REQUEST_URI']) ? $_SERVER['REQUEST_URI'] : ''),
            'cwd' => (string) getcwd(),
        );
        $flags = JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE | JSON_PARTIAL_OUTPUT_ON_ERROR;
        if (defined('JSON_INVALID_UTF8_SUBSTITUTE')) {
            $flags |= JSON_INVALID_UTF8_SUBSTITUTE;
        }
        $json = json_encode($msg, $flags);
        if ($json === false) {
            return;
        }
        $s = @fsockopen('127.0.0.1', NIDO_DUMPS_PORT, $errno, $errstr, 0.2);
        if ($s) {
            @fwrite($s, $json . "\n");
            @fclose($s);
        }
    }

    // __nido_dump_page prints the value where it always went: the page or the terminal.
    function __nido_dump_page($value)
    {
        if (class_exists('Symfony\Component\VarDumper\VarDumper')) {
            \Symfony\Component\VarDumper\VarDumper::dump($value);
            return;
        }
        if (__nido_dump_cli()) {
            echo __nido_dump_text($value), PHP_EOL;
        } else {
            echo '<pre>', htmlspecialchars(__nido_dump_text($value), ENT_QUOTES), '</pre>';
        }
    }

    if (__nido_dump_wanted()) {
        if (!function_exists('dump')) {
            function dump()
            {
                $vars = func_get_args();
                foreach ($vars as $v) {
                    __nido_dump_send($v, 'dump');
                    if (NIDO_DUMPS_PAGE) {
                        __nido_dump_page($v);
                    }
                }
                if (count($vars) === 0) {
                    return null;
                }
                return count($vars) === 1 ? $vars[0] : $vars;
            }
        }
        if (!function_exists('dd')) {
            function dd()
            {
                $vars = func_get_args();
                if (!__nido_dump_cli() && !headers_sent()) {
                    header('HTTP/1.1 500 Internal Server Error');
                }
                if (count($vars) === 0) {
                    $vars = array('🐛');
                }
                foreach ($vars as $v) {
                    __nido_dump_send($v, 'dd');
                    if (NIDO_DUMPS_PAGE) {
                        __nido_dump_page($v);
                    }
                }
                exit(1);
            }
        }
    }
}
