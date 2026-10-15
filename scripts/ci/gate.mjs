import { appendFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

export function assessNeeds(needs) {
  const errors = [];
  const rows = [];
  if (needs.repository?.result !== 'success') {
    errors.push('Repository checks did not succeed.');
  }
  for (const module of ['frontend', 'java', 'go']) {
    const present = needs.repository?.outputs?.[module];
    const result = needs[module]?.result;
    const expected = present === 'true' ? 'success' : present === 'false' ? 'skipped' : undefined;
    if (!expected || result !== expected) {
      errors.push(`${module}: discovered=${present ?? 'missing'}, result=${result ?? 'missing'}, expected=${expected ?? 'valid discovery output'}`);
    }
    rows.push(`| ${module} | ${present ?? 'missing'} | ${result ?? 'missing'} |`);
  }
  return {
    errors,
    summary: ['## CI results', '', '| Module present in Git | Discovered | Job result |', '| --- | --- | --- |', ...rows,
      '', 'Skipped modules have not been built or tested. CI does not verify real platform I/O or deployment to Y7000P.', ''].join('\n'),
  };
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const result = assessNeeds(JSON.parse(process.env.CI_NEEDS_JSON ?? '{}'));
    console.log(result.summary);
    if (process.env.GITHUB_STEP_SUMMARY) appendFileSync(process.env.GITHUB_STEP_SUMMARY, result.summary);
    for (const error of result.errors) console.error(error);
    if (result.errors.length) process.exitCode = 1;
  } catch (error) {
    console.error(`CI Gate could not validate its inputs: ${error.message}`);
    process.exitCode = 1;
  }
}
