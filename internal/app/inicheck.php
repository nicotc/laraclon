<?php
// Nido: comprueba valores de PHP sin aplicarlos. Uso: php inicheck.php <archivo> [perdir]
// Escribe JSON con el error de sintaxis, o los nombres que este PHP no conoce y los que no se
// pueden cambiar por carpeta (.user.ini). Vale para PHP 5.4 en adelante.
$file = $argv[1];
$perdir = isset($argv[2]) && $argv[2] === 'perdir';
$values = @parse_ini_file($file, false);
if ($values === false) {
    $e = error_get_last();
    echo json_encode(array('error' => $e ? $e['message'] : 'no se pudo leer'));
    exit;
}
$known = ini_get_all(null, true);
$out = array('unknown' => array(), 'system' => array());
foreach ($values as $name => $value) {
    if ($name === 'extension' || $name === 'zend_extension') {
        if ($perdir) {
            $out['system'][] = $name;
        }
        continue;
    }
    if (!isset($known[$name])) {
        $out['unknown'][] = $name;
    } elseif ($perdir && !($known[$name]['access'] & (INI_USER | INI_PERDIR))) {
        $out['system'][] = $name;
    }
}
echo json_encode($out);
