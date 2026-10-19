import test from 'node:test';
import assert from 'node:assert/strict';
import {
  parseZpoolList,
  parseZpoolStatus,
  parseZfsPools,
  parseZfsDatasets,
} from './zfsParser.ts';

const SAMPLE_ZPOOL_LIST = `tank\t7.27T\t4.22T\t3.05T\t-\t-\t18%\t58%\t1.00x\tONLINE\t-
boot-pool\t100G\t15.2G\t84.8G\t-\t-\t1%\t15%\t1.00x\tONLINE\t-`;

const SAMPLE_ZPOOL_STATUS_WITH_PROSE = `  pool: tank
 state: ONLINE
status: Some supported and requested features are not enabled on the pool.
	The pool can still be used, but some features are unavailable.
action: Enable all features using 'zpool upgrade'. Once this is done,
	the pool may no longer be accessible by software that does not support
	the features. See zpool-features(7) for details.
  scan: scrub repaired 0B in 04:12:30 with 0 errors on Sun Oct 11 04:36:31 2026
config:

	NAME        STATE     READ WRITE CKSUM
	tank        ONLINE       0     0     0
	  mirror-0  ONLINE       0     0     0
	    sdb     ONLINE       0     0     0
	    sdc     ONLINE       0     0     0
	cache
	  sda       ONLINE       0     0     0

errors: No known data errors

  pool: boot-pool
 state: ONLINE
  scan: scrub repaired 0B in 00:00:14 with 0 errors on Sun Oct 11 03:45:01 2026
config:

	NAME         STATE     READ WRITE CKSUM
	boot-pool    ONLINE       0     0     0
	  nvme0n1p3  ONLINE       0     0     0

errors: No known data errors`;

const SAMPLE_ZFS_LIST = `NAME                USED  AVAIL  REFER  MOUNTPOINT
tank                4.22T  3.05T   128K  /tank
tank/app-data       1.50T  3.05T  1.50T  /tank/app-data
tank/media          2.72T  3.05T  2.72T  /tank/media`;

test('verifies old parsing logic failed by capturing prose lines as pools', () => {
  // Confirm the bug: old parser split lines by whitespace and pushed any line with >= 6 tokens
  const lines = SAMPLE_ZPOOL_STATUS_WITH_PROSE.split('\n').filter(Boolean);
  const oldBuggyPools: string[] = [];
  for (const line of lines) {
    const tokens = line.trim().split(/\s+/);
    if (tokens.length >= 6 && tokens[0] !== 'NAME') {
      oldBuggyPools.push(tokens[0]);
    }
  }

  // The old logic erroneously extracted prose words as pool names:
  assert.ok(oldBuggyPools.includes('status:'));
  assert.ok(oldBuggyPools.includes('The'));
  assert.ok(oldBuggyPools.includes('action:'));
  assert.ok(oldBuggyPools.includes('the'));
});

test('parseZfsPools never treats prose sentences or upgrade warnings as pool names', () => {
  const pools = parseZfsPools(SAMPLE_ZPOOL_LIST, SAMPLE_ZPOOL_STATUS_WITH_PROSE);

  assert.equal(pools.length, 2);
  const names = pools.map(p => p.name);
  assert.deepEqual(names, ['tank', 'boot-pool']);

  const proseWords = ['status:', 'The', 'action:', 'the', 'scan:', 'config:', 'errors:', 'see:'];
  for (const word of proseWords) {
    assert.equal(pools.some(p => p.name.toLowerCase() === word.toLowerCase()), false, `Should not contain prose '${word}'`);
  }
});

test('parseZpoolList accurately parses columns for 11-column and 10-column formats', () => {
  const pools = parseZpoolList(SAMPLE_ZPOOL_LIST);
  assert.equal(pools.length, 2);

  const tank = pools[0];
  assert.equal(tank.name, 'tank');
  assert.equal(tank.size, '7.27T');
  assert.equal(tank.allocated, '4.22T');
  assert.equal(tank.free, '3.05T');
  assert.equal(tank.frag, '18%');
  assert.equal(tank.cap, '58%');
  assert.equal(tank.dedup, '1.00x');
  assert.equal(tank.health, 'ONLINE');
  assert.equal(tank.altroot, '-');

  // Test 10-column format (older OpenZFS without checkpoint column)
  const tenCol = 'tank10\t10.0T\t6.0T\t4.0T\t-\t12%\t60%\t1.00x\tONLINE\t-';
  const pools10 = parseZpoolList(tenCol);
  assert.equal(pools10.length, 1);
  assert.equal(pools10[0].name, 'tank10');
  assert.equal(pools10[0].size, '10.0T');
  assert.equal(pools10[0].allocated, '6.0T');
  assert.equal(pools10[0].free, '4.0T');
  assert.equal(pools10[0].frag, '12%');
  assert.equal(pools10[0].health, 'ONLINE');
});

test('parseZpoolStatus extracts scan scrub status, errors, and real disk devices', () => {
  const statusMap = parseZpoolStatus(SAMPLE_ZPOOL_STATUS_WITH_PROSE);
  assert.ok(statusMap.has('tank'));
  assert.ok(statusMap.has('boot-pool'));

  const tankStatus = statusMap.get('tank')!;
  assert.equal(tankStatus.name, 'tank');
  assert.equal(tankStatus.state, 'ONLINE');
  assert.match(tankStatus.scan, /scrub repaired 0B/);
  assert.match(tankStatus.scan, /0 errors/);
  assert.equal(tankStatus.errors, 'No known data errors');

  // Vdev & device checks
  assert.ok(tankStatus.devices.length >= 3);
  const devNames = tankStatus.devices.map(d => d.name);
  assert.ok(devNames.includes('sdb'));
  assert.ok(devNames.includes('sdc'));
  assert.ok(devNames.includes('sda'));

  const sda = tankStatus.devices.find(d => d.name === 'sda');
  assert.equal(sda?.role, 'cache');

  // Summary should reflect mirror and cache
  assert.match(tankStatus.vdevType, /mirror-0/i);
  assert.match(tankStatus.vdevType, /cache/i);

  const bootStatus = statusMap.get('boot-pool')!;
  assert.equal(bootStatus.devices[0].name, 'nvme0n1p3');
});

test('parseZfsPools combines list metrics with status vdev topology and scrub status', () => {
  const pools = parseZfsPools(SAMPLE_ZPOOL_LIST, SAMPLE_ZPOOL_STATUS_WITH_PROSE);
  const tank = pools.find(p => p.name === 'tank')!;

  assert.equal(tank.name, 'tank');
  assert.equal(tank.size, '7.27T');
  assert.equal(tank.allocated, '4.22T');
  assert.equal(tank.free, '3.05T');
  assert.equal(tank.health, 'ONLINE');
  assert.match(tank.scrubStatus, /repaired 0B/);
  assert.match(tank.vdevType, /mirror-0/);

  // Verified actual devices are populated
  assert.ok(tank.devices && tank.devices.length >= 3);
  assert.ok(tank.devices.some(d => d.name === 'sdb'));
  assert.ok(tank.devices.some(d => d.name === 'sdc'));
  assert.ok(tank.devices.some(d => d.name === 'sda' && d.role === 'cache'));
});

test('parseZfsPools handles scrub in progress and resilvering', () => {
  const statusInProg = `  pool: datapool
 state: ONLINE
  scan: scrub in progress since Sun Oct 18 20:00:00 2026
	2.40T scanned at 450M/s, 1.20T issued at 225M/s, 4.80T total
	0B repaired, 25.00% done, 02:30:00 to go
config:

	NAME        STATE     READ WRITE CKSUM
	datapool    ONLINE       0     0     0
	  sda       ONLINE       0     0     0

errors: No known data errors`;

  const list = 'datapool\t4.80T\t1.20T\t3.60T\t-\t-\t5%\t25%\t1.00x\tONLINE\t-';
  const pools = parseZfsPools(list, statusInProg);
  assert.equal(pools.length, 1);
  assert.match(pools[0].scrubStatus, /in progress|25\.00% done/i);
});

test('parseZfsPools gracefully handles empty or non-ZFS host outputs', () => {
  assert.deepEqual(parseZfsPools('', ''), []);
  assert.deepEqual(parseZfsPools('NO_ZPOOL_LIST', 'NO_ZPOOL'), []);
  assert.deepEqual(parseZfsPools('no pools available', 'no pools available'), []);
  assert.deepEqual(parseZfsPools(undefined as any, undefined as any), []);
});

test('parseZfsDatasets correctly parses zfs list output and skips headers and errors', () => {
  const datasets = parseZfsDatasets(SAMPLE_ZFS_LIST);
  assert.equal(datasets.length, 3);
  assert.equal(datasets[0].name, 'tank');
  assert.equal(datasets[0].mount, '/tank');
  assert.equal(datasets[1].name, 'tank/app-data');
  assert.equal(datasets[1].used, '1.50T');
  assert.equal(datasets[2].name, 'tank/media');
  assert.equal(datasets[2].used, '2.72T');

  // Error handling
  assert.deepEqual(parseZfsDatasets(''), []);
  assert.deepEqual(parseZfsDatasets('NO_ZFS'), []);
  assert.deepEqual(parseZfsDatasets('no datasets available'), []);
});
