#!/usr/bin/env python3
"""Synthetic fixtures only. No router, provider credentials or live Xray."""
import json
import os
import pathlib
import resource
import signal
import subprocess
import threading
import time
import urllib.request

BASE = pathlib.Path('/tmp/fixture')
BASE.mkdir(mode=0o700)
OUT = pathlib.Path('/measurements')
BIN = pathlib.Path('/fixtures')
core = {
    'instance': {'role': 'controller'},
    'paths': {key: str(BASE / name) for key, name in (
        ('state_dir', 'state'), ('cache_dir', 'cache'), ('run_dir', 'run'), ('log_file', 'core.log'))},
    'platform': {'kind': 'keenetic', 'xray_status_command': ['/bin/true']},
    'api': {'enabled': True, 'listen': '127.0.0.1:19443', 'unix_socket': str(BASE / 'run/control.sock'),
            'tls': {'enabled': False}},
    'web': {'enabled': False, 'credentials_file': str(BASE / 'credentials.json')},
    'xray': {'config_dir': str(BASE / 'xray'), 'managed_dir': str(BASE / 'xray'),
             'base_routing_file': str(BASE / 'xray/routing.json')},
    'subscriptions': {'sources': [{'id': 'offline', 'url': 'https://unused.example.invalid/subscription', 'enabled': False}], 'max_sources': 2, 'max_nodes': 50, 'max_nodes_per_source': 50,
                      'max_response_bytes': '512KiB'},
    'targets': [{'id': name, 'url': 'https://'+name+'.example.invalid/probe', 'role': role,
                 'weight': 1, 'policy': '2xx3xx', 'max_response_bytes': 4096} for name, role in
                [('score', 'score'), ('health1', 'health'), ('health2', 'health')]],
    'pool': {'size': 2},
    'benchmark': {'batch_size': 2, 'latency_workers': 2, 'speed': {'enabled': False}},
}
ui = {'instance': {'role': 'ui'}, 'platform': {'kind': 'keenetic'},
      'web': {'enabled': True, 'listen': '127.0.0.1:19444', 'tls': {'enabled': False}},
      'ui': {'enabled': True, 'upstream': 'http://127.0.0.1:19443'},
      'paths': {'state_dir': '/must-not-create-state', 'cache_dir': '/must-not-create-cache',
                'run_dir': '/must-not-create-run', 'log_file': str(BASE / 'ui.log')}}
def yaml_mapping(value, indent=0):
    lines = []
    for key, item in value.items():
        prefix = ' ' * indent + key + ':'
        if isinstance(item, dict) and item:
            lines.extend([prefix, yaml_mapping(item, indent+2)])
        elif isinstance(item, list) and item and isinstance(item[0], dict):
            lines.append(prefix)
            for entry in item:
                rows = yaml_mapping(entry, indent+4).splitlines()
                rows[0] = ' '*(indent+2)+'- '+rows[0].lstrip()
                lines.extend(rows)
        else:
            lines.append(prefix + ' ' + json.dumps(item))
    return '\n'.join(lines)

for name, cfg in (('core', core), ('ui', ui)):
    (BASE / f'{name}.json').write_text(yaml_mapping(cfg)+'\n')

def command(args, **kwargs):
    result = subprocess.run([str(BIN / args[0]), *args[1:]], timeout=180,
                            stdout=subprocess.PIPE, stderr=subprocess.STDOUT, **kwargs)
    if result.returncode:
        raise RuntimeError('synthetic fixture command failed: ' + result.stdout.decode(errors='replace'))
    return result

command(['kee-route-managerctl', 'passwd', '--config', str(BASE / 'core.json'),
         '--username', 'admin', '--password-stdin'], input=b'synthetic-fixture-only\n')
processes = []
metrics = {}
global_metrics = {'peak_process_count': 0}
stop = threading.Event()

def sample():
    while not stop.wait(0.02):
        global_metrics['peak_process_count'] = max(global_metrics['peak_process_count'],
                                                   len(list(pathlib.Path('/proc').glob('[0-9]*'))))
        for name, process in list(processes):
            try:
                status = pathlib.Path(f'/proc/{process.pid}/status').read_text()
                fields = {s.split(':', 1)[0]: s.split(':', 1)[1].strip() for s in status.splitlines() if ':' in s}
                item = metrics.setdefault(name, {'peak_rss_kib': 0, 'peak_pss_kib': 0, 'peak_threads': 0})
                item['peak_rss_kib'] = max(item['peak_rss_kib'], int(fields.get('VmRSS', '0').split()[0]))
                item['peak_threads'] = max(item['peak_threads'], int(fields.get('Threads', '0')))
                rollup = pathlib.Path(f'/proc/{process.pid}/smaps_rollup')
                if rollup.exists():
                    for line in rollup.read_text().splitlines():
                        if line.startswith('Pss:'):
                            item['peak_pss_kib'] = max(item['peak_pss_kib'], int(line.split()[1]))
                stat = pathlib.Path(f'/proc/{process.pid}/stat').read_text().rsplit(')', 1)[1].split()
                item['cpu_seconds'] = (int(stat[11]) + int(stat[12])) / os.sysconf('SC_CLK_TCK')
            except (FileNotFoundError, ProcessLookupError, PermissionError):
                pass

def start(name, args):
    log = open(OUT / f'{name}.log', 'wb')
    p = subprocess.Popen([str(BIN / args[0]), *args[1:]], stdout=log, stderr=subprocess.STDOUT)
    log.close()
    processes.append((name, p))
    return p

thread = threading.Thread(target=sample, daemon=True)
thread.start()
started = time.monotonic()
try:
    daemon = start('daemon', ['kee-route-managerd', 'serve', '--config', str(BASE / 'core.json')])
    for _ in range(100):
        result = subprocess.run([str(BIN / 'kee-route-managerctl'), 'ready', '--config', str(BASE / 'core.json')],
                                stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=3)
        if result.returncode == 0:
            break
        if daemon.poll() is not None:
            raise RuntimeError('daemon failed; inspect synthetic daemon.log')
        time.sleep(.05)
    else:
        raise RuntimeError('daemon did not become ready')
    time.sleep(1)
    start('ui', ['kee-route-manager-ui', 'serve', '--config', str(BASE / 'ui.json')])
    for _ in range(100):
        try:
            with urllib.request.urlopen('http://127.0.0.1:19444/assets/app.js', timeout=1) as response:
                response.read()
            break
        except OSError:
            time.sleep(.05)
    else:
        raise RuntimeError('UI did not become ready')
    for _ in range(30):
        with urllib.request.urlopen('http://127.0.0.1:19444/assets/app.js', timeout=1) as response:
            response.read()
    tests = [
        ('subscription', ['subscription.test', '-test.run=Test', '-test.bench=BenchmarkParsePayload/.*/nodes-(50|500)$',
                          '-test.benchmem', '-test.benchtime=3x', '-test.memprofile=/measurements/subscription.heap']),
        ('bench', ['bench.test', '-test.run=Test', '-test.memprofile=/measurements/bench.heap']),
        ('xray', ['xray.test', '-test.run=Test.*(Cancel|Cleanup|Crash|Bound|Batch)',
                  '-test.memprofile=/measurements/xray.heap']),
    ]
    for name, args in tests:
        p = start(name, args)
        if p.wait(timeout=180):
            raise RuntimeError(f'{name} tests failed; inspect synthetic log')
    for name, p in processes[:2]:
        if p.poll() is not None:
            raise RuntimeError(f'{name} stopped unexpectedly')
    time.sleep(1)
finally:
    for _, p in reversed(processes):
        if p.poll() is None:
            p.send_signal(signal.SIGTERM)
            try:
                p.wait(timeout=15)
            except subprocess.TimeoutExpired:
                p.kill()
                p.wait()
    stop.set()
    thread.join(timeout=2)

def cgroup(name):
    path = pathlib.Path('/sys/fs/cgroup') / name
    return path.read_text().strip() if path.exists() else 'unavailable'

print(json.dumps({'scope': 'synthetic amd64 fixture, no live Xray/KeeneticOS/VPN; sampled peaks may miss short spikes',
                  'memory_limit_bytes': cgroup('memory.max'), 'cgroup_peak_bytes': cgroup('memory.peak'),
                  'memory_events': cgroup('memory.events'), 'processes': metrics,
                  **global_metrics,
                  'elapsed_seconds': round(time.monotonic()-started, 3),
                  'children_cpu_seconds': round(resource.getrusage(resource.RUSAGE_CHILDREN).ru_utime +
                                                resource.getrusage(resource.RUSAGE_CHILDREN).ru_stime, 3)}, indent=2))
