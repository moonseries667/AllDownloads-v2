import assert from 'node:assert/strict';
import test from 'node:test';
import { assessNeeds } from './gate.mjs';

function needs(outputs, results, repository = 'success') {
  return { repository: { result: repository, outputs },
    frontend: { result: results.frontend }, java: { result: results.java }, go: { result: results.go } };
}

test('documentation-only and successful mixed-module runs pass', () => {
  assert.deepEqual(assessNeeds(needs(
    { frontend: 'false', java: 'false', go: 'false' },
    { frontend: 'skipped', java: 'skipped', go: 'skipped' },
  )).errors, []);
  assert.deepEqual(assessNeeds(needs(
    { frontend: 'true', java: 'false', go: 'true' },
    { frontend: 'success', java: 'skipped', go: 'success' },
  )).errors, []);
});

test('failure, cancellation or skipped checks for an existing module block the gate', () => {
  for (const status of ['failure', 'cancelled', 'skipped', undefined]) {
    const result = assessNeeds(needs(
      { frontend: 'true', java: 'false', go: 'false' },
      { frontend: status, java: 'skipped', go: 'skipped' },
    ));
    assert.equal(result.errors.length, 1, status);
  }
});

test('failed discovery and missing or invalid outputs cannot produce a green gate', () => {
  assert.ok(assessNeeds({}).errors.length > 0);
  assert.ok(assessNeeds(needs(
    { frontend: 'false', java: 'false', go: 'false' },
    { frontend: 'skipped', java: 'skipped', go: 'skipped' }, 'failure',
  )).errors.length > 0);
  assert.ok(assessNeeds(needs(
    { frontend: 'maybe', java: 'false', go: 'false' },
    { frontend: 'skipped', java: 'skipped', go: 'skipped' },
  )).errors.length > 0);
});
