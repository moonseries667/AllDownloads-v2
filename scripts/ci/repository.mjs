import { execFileSync } from 'node:child_process';
import {
  appendFileSync,
  existsSync,
  lstatSync,
  readFileSync,
  readlinkSync,
} from 'node:fs';
import path from 'node:path';
import { TextDecoder } from 'node:util';
import { fileURLToPath } from 'node:url';

const binaryExtensions = new Set([
  '.7z', '.avi', '.bmp', '.class', '.dll', '.doc', '.docx', '.exe', '.gif',
  '.gz', '.ico', '.jar', '.jpeg', '.jpg', '.mp3', '.mp4', '.pdf', '.png',
  '.so', '.tar', '.wasm', '.webp', '.woff', '.woff2', '.zip',
]);

const frontendSourceExtensions = new Set([
  '.astro', '.cjs', '.css', '.html', '.js', '.jsx', '.less', '.mjs', '.sass',
  '.scss', '.svelte', '.ts', '.tsx', '.vue',
]);

const decoder = new TextDecoder('utf-8', { fatal: true });

function displayPath(filePath) {
  return filePath.split(path.sep).join('/');
}

function readTrackedText(root, relativePath) {
  const absolutePath = path.join(root, ...relativePath.split('/'));
  const stat = lstatSync(absolutePath);
  const bytes = stat.isSymbolicLink()
    ? Buffer.from(readlinkSync(absolutePath), 'utf8')
    : readFileSync(absolutePath);

  if (bytes.includes(0) || binaryExtensions.has(path.extname(relativePath).toLowerCase())) {
    return null;
  }

  return decoder.decode(bytes);
}

function removeFencedCode(markdown, relativePath, diagnostics) {
  const lines = markdown.split(/\r?\n/);
  let openFence = null;

  for (let index = 0; index < lines.length; index += 1) {
    const line = lines[index];
    const fenceMatch = line.match(/^ {0,3}(`{3,}|~{3,})(.*)$/);

    if (openFence === null && fenceMatch) {
      openFence = {
        marker: fenceMatch[1][0],
        width: fenceMatch[1].length,
        line: index + 1,
      };
      lines[index] = '';
      continue;
    }

    if (openFence !== null) {
      const closePattern = new RegExp(`^ {0,3}${openFence.marker}{${openFence.width},}\\s*$`);
      if (closePattern.test(line)) openFence = null;
      lines[index] = '';
    }
  }

  if (openFence !== null) {
    diagnostics.push(`${relativePath}:${openFence.line}: unclosed ${openFence.marker.repeat(openFence.width)} code fence`);
  }

  return lines.join('\n');
}

function removeInlineCode(markdown) {
  const characters = [...markdown];
  let index = 0;

  while (index < characters.length) {
    if (characters[index] !== '`') {
      index += 1;
      continue;
    }

    let width = 1;
    while (characters[index + width] === '`') width += 1;
    let closing = index + width;
    let closingStart = -1;

    while (closing < characters.length) {
      if (characters[closing] !== '`') {
        closing += 1;
        continue;
      }

      let closingWidth = 1;
      while (characters[closing + closingWidth] === '`') closingWidth += 1;
      if (closingWidth === width) {
        closingStart = closing;
        break;
      }
      closing += closingWidth;
    }

    if (closingStart < 0) {
      index += width;
      continue;
    }

    for (let cursor = index; cursor < closingStart + width; cursor += 1) {
      if (characters[cursor] !== '\n') characters[cursor] = ' ';
    }
    index = closingStart + width;
  }

  return characters.join('');
}

function markdownDestinations(markdown) {
  const links = [];
  const patterns = [
    /!?\[[^\]]*\]\(\s*(<[^>\r\n]*>|(?:\\.|[^)\s]|\([^)]*\))+)(?:\s+(?:"[^"]*"|'[^']*'|\([^)]*\)))?\s*\)/g,
    /^\s*\[[^\]]+\]:\s*(<[^>\r\n]*>|\S+)/gm,
  ];

  for (const pattern of patterns) {
    for (const match of markdown.matchAll(pattern)) {
      const destination = match[1].startsWith('<') && match[1].endsWith('>')
        ? match[1].slice(1, -1)
        : match[1];
      const line = markdown.slice(0, match.index).split('\n').length;
      links.push({ destination, line });
    }
  }

  return links;
}

function resolveMarkdownLink(root, markdownPath, destination) {
  const target = destination.trim();
  if (
    target.length === 0
    || target.startsWith('#')
    || target.startsWith('//')
    || /^[a-z][a-z\d+.-]*:/i.test(target)
    || target.startsWith('/')
  ) return { skipped: true };

  const withoutQueryOrFragment = target.split(/[?#]/, 1)[0];
  if (withoutQueryOrFragment.length === 0) return { skipped: true };

  let decodedPath;
  try {
    decodedPath = decodeURIComponent(withoutQueryOrFragment);
  } catch {
    return { error: 'has invalid URL encoding' };
  }

  const normalizedTarget = decodedPath.replaceAll('\\', '/');
  const absoluteTarget = path.resolve(
    root,
    path.dirname(markdownPath),
    ...normalizedTarget.split('/'),
  );
  const rootRelativeTarget = path.relative(root, absoluteTarget);
  if (rootRelativeTarget === '..' || rootRelativeTarget.startsWith(`..${path.sep}`) || path.isAbsolute(rootRelativeTarget)) {
    return { error: 'escapes the repository root' };
  }
  if (!existsSync(absoluteTarget)) return { error: 'does not exist' };

  return { skipped: false, absoluteTarget };
}

function checkMarkdown(root, relativePath, markdown, diagnostics) {
  const withoutFences = removeFencedCode(markdown, relativePath, diagnostics);
  const visibleMarkdown = removeInlineCode(withoutFences);

  for (const { destination, line } of markdownDestinations(visibleMarkdown)) {
    const result = resolveMarkdownLink(root, relativePath, destination);
    if (result.error) {
      diagnostics.push(`${relativePath}:${line}: relative link ${JSON.stringify(destination)} ${result.error}`);
    }
  }
}

function underModule(paths, modulePath) {
  const prefix = `${modulePath}/`;
  return paths.filter((trackedPath) => trackedPath.startsWith(prefix));
}

export function discoverModules(trackedPaths) {
  const frontendFiles = underModule(trackedPaths, 'frontend');
  const javaFiles = underModule(trackedPaths, 'backend/java');
  const goFiles = underModule(trackedPaths, 'backend/go');

  return {
    frontend: frontendFiles.some((file) =>
      frontendSourceExtensions.has(path.extname(file).toLowerCase())
      || ['frontend/package.json', 'frontend/package-lock.json'].includes(file)),
    java: javaFiles.some((file) =>
      path.extname(file).toLowerCase() === '.java'
      || ['backend/java/pom.xml', 'backend/java/mvnw.cmd'].includes(file)),
    go: goFiles.some((file) => path.extname(file).toLowerCase() === '.go' || file === 'backend/go/go.mod'),
  };
}

function checkModuleRequirements(root, trackedPaths, modules, diagnostics, log) {
  const tracked = new Set(trackedPaths.map(displayPath));
  const outputs = { ...modules };
  const specs = [
    {
      key: 'frontend',
      requirements: ['frontend/package.json', 'frontend/package-lock.json'],
      validate(rootPath) {
        const packagePath = path.join(rootPath, 'frontend', 'package.json');
        let manifest;
        try {
          manifest = JSON.parse(readFileSync(packagePath, 'utf8'));
        } catch {
          return ['frontend/package.json is missing or invalid JSON'];
        }
        const issues = [];
        if (typeof manifest.scripts?.build !== 'string' || manifest.scripts.build.trim() === '') {
          issues.push('frontend/package.json is missing scripts.build');
        }
        if (typeof manifest.scripts?.test !== 'string' || manifest.scripts.test.trim() === '') {
          issues.push('frontend/package.json is missing scripts.test');
        }
        return issues;
      },
    },
    {
      key: 'java',
      requirements: [
        'backend/java/pom.xml',
        'backend/java/mvnw.cmd',
        'backend/java/.mvn/wrapper/maven-wrapper.properties',
      ],
    },
    {
      key: 'go',
      requirements: ['backend/go/go.mod'],
    },
  ];

  for (const spec of specs) {
    if (!modules[spec.key]) {
      log(`[SKIP] ${spec.key}: no tracked implementation; skipped, not tested.`);
      continue;
    }

    const missing = spec.requirements.filter((file) => !tracked.has(file));
    const extraIssues = spec.validate ? spec.validate(root) : [];
    const issues = [
      ...missing.map((file) => `missing ${file}`),
      ...extraIssues,
    ];

    if (issues.length > 0) {
      outputs[spec.key] = true;
      diagnostics.push(...issues.map((issue) => `${spec.key}: ${issue}`));
      log(`[FAIL] ${spec.key}: implementation found, but module checks are incomplete.`);
    } else {
      log(`[READY] ${spec.key}: required build/test entry points found; build and tests not run.`);
    }
  }

  return outputs;
}

export function checkRepository({ root, trackedPaths, outputPath, log = () => {} }) {
  const diagnostics = [];
  const normalizedPaths = [...new Set(trackedPaths.map(displayPath))];

  for (const relativePath of normalizedPaths) {
    let text;
    try {
      text = readTrackedText(root, relativePath);
    } catch (error) {
      const issue = error instanceof TypeError && error.message.includes('encoding utf-8')
        ? 'invalid UTF-8'
        : `could not read tracked file (${error.message})`;
      diagnostics.push(`${relativePath}: ${issue}`);
      continue;
    }

    if (text === null) continue;
    if (path.extname(relativePath).toLowerCase() === '.md') {
      checkMarkdown(root, relativePath, text, diagnostics);
    }
  }

  const modules = discoverModules(normalizedPaths);
  const outputs = checkModuleRequirements(root, normalizedPaths, modules, diagnostics, log);

  if (outputPath) {
    const lines = Object.entries(outputs).map(([name, present]) => `${name}=${present}`);
    appendFileSync(outputPath, `${lines.join('\n')}\n`, 'utf8');
  }

  return { diagnostics, modules: outputs };
}

function main() {
  const root = process.cwd();
  const trackedPaths = execFileSync('git', ['ls-files', '-z'], {
    cwd: root,
    encoding: 'utf8',
  }).split('\0').filter(Boolean);

  const result = checkRepository({
    root,
    trackedPaths,
    outputPath: process.env.GITHUB_OUTPUT,
    log: console.log,
  });

  if (result.diagnostics.length > 0) {
    for (const diagnostic of result.diagnostics) console.error(`[FAIL] ${diagnostic}`);
    process.exitCode = 1;
    return;
  }

  console.log('Repository structure checks passed; module builds and tests were not run.');
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    main();
  } catch (error) {
    console.error(`[FAIL] repository check could not run: ${error.message}`);
    process.exitCode = 1;
  }
}
