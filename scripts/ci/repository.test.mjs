import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { checkRepository, discoverModules } from './repository.mjs';

function fixture(t) {
  const root = mkdtempSync(path.join(os.tmpdir(), 'repository-check-'));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  return {
    root,
    write(relativePath, content) {
      const absolutePath = path.join(root, ...relativePath.split('/'));
      mkdirSync(path.dirname(absolutePath), { recursive: true });
      writeFileSync(absolutePath, content);
      return relativePath;
    },
    writeBytes(relativePath, bytes) {
      const absolutePath = path.join(root, ...relativePath.split('/'));
      mkdirSync(path.dirname(absolutePath), { recursive: true });
      writeFileSync(absolutePath, bytes);
      return relativePath;
    },
  };
}

test('empty and documentation-only modules are skipped and reported as false', (t) => {
  const temp = fixture(t);
  const trackedPaths = [
    temp.write('frontend/README.md', 'Frontend notes.\n'),
    temp.write('frontend/docs/overview.md', 'Documentation.\n'),
    temp.write('backend/java/.gitkeep', ''),
    temp.write('backend/go/README', 'Go notes.\n'),
  ];
  const messages = [];
  const outputPath = path.join(temp.root, 'github-output.txt');

  const result = checkRepository({ root: temp.root, trackedPaths, outputPath, log: (line) => messages.push(line) });

  assert.deepEqual(result.modules, { frontend: false, java: false, go: false });
  assert.deepEqual(result.diagnostics, []);
  assert.equal(messages.length, 3);
  assert.ok(messages.every((line) => line.includes('skipped, not tested')));
  assert.equal(readFileSync(outputPath, 'utf8'), 'frontend=false\njava=false\ngo=false\n');
  assert.deepEqual(discoverModules(trackedPaths), { frontend: false, java: false, go: false });
});

test('implemented modules require their declared build and test entry points', (t) => {
  const temp = fixture(t);
  const trackedPaths = [
    temp.write('frontend/src/app.tsx', 'export const app = true;\n'),
    temp.write('frontend/package.json', JSON.stringify({ name: 'partial-ui', scripts: {} })),
    temp.write('backend/java/src/main/App.java', 'class App {}\n'),
    temp.write('backend/go/main.go', 'package main\n'),
  ];
  const messages = [];

  const result = checkRepository({ root: temp.root, trackedPaths, log: (line) => messages.push(line) });

  assert.deepEqual(result.modules, { frontend: true, java: true, go: true });
  assert.ok(result.diagnostics.some((line) => line.includes('frontend/package.json')));
  assert.ok(result.diagnostics.some((line) => line.includes('frontend/package-lock.json')));
  assert.ok(result.diagnostics.some((line) => line.includes('scripts.build')));
  assert.ok(result.diagnostics.some((line) => line.includes('scripts.test')));
  assert.ok(result.diagnostics.some((line) => line.includes('backend/java/pom.xml')));
  assert.ok(result.diagnostics.some((line) => line.includes('mvnw.cmd')));
  assert.ok(result.diagnostics.some((line) => line.includes('maven-wrapper.properties')));
  assert.ok(result.diagnostics.some((line) => line.includes('backend/go/go.mod')));
  assert.ok(messages.every((line) => line.includes('implementation found')));
});

test('incomplete descriptors and Go test-only modules are discovered', (t) => {
  const temp = fixture(t);
  const trackedPaths = [
    temp.write('frontend/package.json', JSON.stringify({ name: 'partial-ui' })),
    temp.write('backend/java/pom.xml', '<project></project>\n'),
    temp.write('backend/go/handlers_test.go', 'package handlers\n'),
  ];

  const result = checkRepository({ root: temp.root, trackedPaths });

  assert.deepEqual(discoverModules(trackedPaths), { frontend: true, java: true, go: true });
  assert.deepEqual(result.modules, { frontend: true, java: true, go: true });
  assert.ok(result.diagnostics.some((line) => line.includes('frontend/package-lock.json')));
  assert.ok(result.diagnostics.some((line) => line.includes('scripts.build')));
  assert.ok(result.diagnostics.some((line) => line.includes('backend/java/mvnw.cmd')));
  assert.ok(result.diagnostics.some((line) => line.includes('backend/go/go.mod')));
});

test('valid modules and encoded relative Markdown links with fragments pass', (t) => {
  const temp = fixture(t);
  const trackedPaths = [
    temp.write('README.md', [
      '# Project',
      '[Guide](docs/Quick%20Start.md#install)',
      '[External](https://example.com/guide)',
      '[Email](mailto:team@example.com)',
      '[Section](#project)',
      '`[example](missing.md)`',
      '```md',
      '[sample](also-missing.md)',
      '```',
      '',
    ].join('\n')),
    temp.write('docs/Quick Start.md', '# Guide\n'),
    temp.write('frontend/src/app.tsx', 'export const app = true;\n'),
    temp.write('frontend/package.json', JSON.stringify({ scripts: { build: 'vite build', test: 'node --test' } })),
    temp.write('frontend/package-lock.json', JSON.stringify({ lockfileVersion: 3 })),
    temp.write('backend/java/src/main/App.java', 'class App {}\n'),
    temp.write('backend/java/pom.xml', '<project></project>\n'),
    temp.write('backend/java/mvnw.cmd', '@echo off\n'),
    temp.write('backend/java/.mvn/wrapper/maven-wrapper.properties', 'distributionUrl=https://example.invalid\n'),
    temp.write('backend/go/main.go', 'package main\n'),
    temp.write('backend/go/go.mod', 'module example.invalid/app\n\ngo 1.25\n'),
  ];
  const messages = [];

  const result = checkRepository({ root: temp.root, trackedPaths, log: (line) => messages.push(line) });

  assert.deepEqual(result.diagnostics, []);
  assert.deepEqual(result.modules, { frontend: true, java: true, go: true });
  assert.equal(messages.filter((line) => line.startsWith('[READY]')).length, 3);
});

test('broken relative links, unclosed fences, and malformed tracked UTF-8 fail', (t) => {
  const temp = fixture(t);
  const trackedPaths = [
    temp.write('README.md', [
      '[Broken](missing%20page.md#section)',
      '~~~js',
      'const open = true;',
      '',
    ].join('\n')),
    temp.writeBytes('notes.txt', Buffer.from([0x66, 0x80, 0x67])),
  ];

  const result = checkRepository({ root: temp.root, trackedPaths });

  assert.ok(result.diagnostics.some((line) => line.includes('README.md:1: relative link')));
  assert.ok(result.diagnostics.some((line) => line.includes('unclosed ~~~ code fence')));
  assert.ok(result.diagnostics.some((line) => line.includes('notes.txt: invalid UTF-8')));
});
