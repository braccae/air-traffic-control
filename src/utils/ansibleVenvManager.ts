import fs from 'node:fs';
import path from 'node:path';
import os from 'node:os';
import { execSync } from 'node:child_process';

export interface AnsibleVenvInfo {
  venvPath: string;
  binDir: string;
  pythonBinary: string;
  ansibleBinary: string;
  ansiblePlaybookBinary: string;
  ansibleVaultBinary: string;
  installedPackages: string[];
}

export const REQUIRED_VENV_PACKAGES: string[] = [
  'ansible-core',
  'pywinrm',
  'requests',
  'jmespath',
  'cryptography',
  'netaddr',
];

/**
 * Returns the resolved path to the persistent Ansible virtual environment.
 * Evaluates process.env.ANSIBLE_VENV_PATH first, falling back to data/ansible-venv.
 */
export function getAnsibleVenvPath(): string {
  return process.env.ANSIBLE_VENV_PATH || path.resolve(process.cwd(), 'data', 'ansible-venv');
}

/**
 * Finds uv on the host system or bootstraps/downloads it into data/bin/uv.
 */
export async function findOrBootstrapUv(options?: {
  customDataDir?: string;
  downloadIfMissing?: boolean;
}): Promise<string> {
  const dataDir = options?.customDataDir || path.resolve(process.cwd(), 'data');
  const localUvPath = path.join(dataDir, 'bin', 'uv');

  // 1. Check local data/bin/uv first
  if (fs.existsSync(localUvPath)) {
    return localUvPath;
  }

  // 2. Check which uv
  try {
    const whichOut = execSync('which uv', {
      encoding: 'utf-8',
      stdio: ['ignore', 'pipe', 'ignore'],
      env: process.env,
    }).trim();
    if (whichOut && fs.existsSync(whichOut)) {
      return whichOut;
    }
  } catch {
    // which failed or uv not on PATH
  }

  // 3. Check known standard fallback paths
  const home = process.env.HOME || os.homedir();
  const candidatePaths = [
    path.join(home, '.local', 'bin', 'uv'),
    path.join(home, '.cargo', 'bin', 'uv'),
  ];

  if (process.env.PATH) {
    const envDirs = process.env.PATH.split(path.delimiter);
    for (const dir of envDirs) {
      if (dir.trim()) {
        candidatePaths.push(path.join(dir.trim(), 'uv'));
      }
    }
  }

  for (const candidate of candidatePaths) {
    if (fs.existsSync(candidate)) {
      return candidate;
    }
  }

  // 4. If missing and download enabled (default true)
  const downloadEnabled = options?.downloadIfMissing !== false;
  if (!downloadEnabled) {
    throw new Error('uv binary not found on PATH, home directories, or local data/bin');
  }

  // Download official uv standalone installer/binary into data/bin
  const binDir = path.join(dataDir, 'bin');
  fs.mkdirSync(binDir, { recursive: true });

  // Use official installer targeting CARGO_HOME / custom bin
  try {
    const installScript = `curl -LsSf https://astral.sh/uv/install.sh | env CARGO_HOME="${dataDir}" UV_INSTALL_DIR="${binDir}" sh`;
    execSync(installScript, {
      stdio: ['ignore', 'pipe', 'pipe'],
      timeout: 60000,
    });
  } catch (err: any) {
    // If installer script fails or curl not present, throw informative error
    throw new Error(`Failed to bootstrap uv standalone binary: ${err?.message || String(err)}`);
  }

  if (fs.existsSync(localUvPath)) {
    try {
      fs.chmodSync(localUvPath, 0o755);
    } catch {
      // ignore chmod error
    }
    return localUvPath;
  }

  throw new Error(`uv installation completed but binary not found at ${localUvPath}`);
}

/**
 * Ensures the persistent Ansible virtual environment exists with all required dependencies.
 * If data/ansible-venv/bin/ansible is missing, runs uv venv and uv pip install.
 */
export async function ensureAnsibleVenv(options?: {
  uvPath?: string;
  venvPath?: string;
}): Promise<AnsibleVenvInfo> {
  const venvPath = options?.venvPath || getAnsibleVenvPath();
  const binDir = path.join(venvPath, 'bin');
  const ansibleBinary = path.join(binDir, 'ansible');
  const ansiblePlaybookBinary = path.join(binDir, 'ansible-playbook');
  const ansibleVaultBinary = path.join(binDir, 'ansible-vault');
  const pythonBinary = path.join(binDir, 'python');

  const ansibleAlreadyExists = fs.existsSync(ansibleBinary);

  if (!ansibleAlreadyExists) {
    const uvPath = options?.uvPath || (await findOrBootstrapUv());

    // 1. Create venv: ${uvPath} venv ${venvPath}
    execSync(`"${uvPath}" venv "${venvPath}"`, {
      stdio: ['ignore', 'pipe', 'pipe'],
      timeout: 120000,
    });

    // 2. Install packages: ${uvPath} pip install --python ${binDir}/python <packages>
    const packageArgs = REQUIRED_VENV_PACKAGES.join(' ');
    execSync(`"${uvPath}" pip install --python "${pythonBinary}" ${packageArgs}`, {
      stdio: ['ignore', 'pipe', 'pipe'],
      timeout: 300000,
    });
  }

  return {
    venvPath,
    binDir,
    pythonBinary,
    ansibleBinary,
    ansiblePlaybookBinary,
    ansibleVaultBinary,
    installedPackages: [...REQUIRED_VENV_PACKAGES],
  };
}

/**
 * Returns environment variables with the persistent venv bin directory prepended to PATH
 * and VIRTUAL_ENV set to the venv path.
 */
export function getAnsibleExecutionEnv(baseEnv?: NodeJS.ProcessEnv): Record<string, string> {
  const venvPath = getAnsibleVenvPath();
  const venvBin = path.join(venvPath, 'bin');
  const basePath = baseEnv?.PATH || '';

  const cleanEnv: Record<string, string> = {};
  if (baseEnv) {
    for (const [key, value] of Object.entries(baseEnv)) {
      if (value !== undefined) {
        cleanEnv[key] = value;
      }
    }
  }

  return {
    ...cleanEnv,
    PATH: basePath ? `${venvBin}:${basePath}` : venvBin,
    VIRTUAL_ENV: venvPath,
  };
}
