import { writeFileSync } from 'node:fs';
import type { Reporter, TestCase, TestResult } from '@playwright/test/reporter';
import { PROVIDE_ANNOTATION_PREFIX } from './annotations';
import { RESULT_ENV, SCHEMA_VERSION } from './protocol';
import type { PlaydagArtifact, PlaydagResponse, Status } from './protocol';

const ARTIFACT_TYPES = ['trace', 'video', 'screenshot'];

/** Maps Playwright's outcome onto a protocol status. */
function toStatus(result: TestResult): Status {
  switch (result.status) {
    case 'passed':
      return 'PASSED';
    case 'skipped':
      return 'SKIPPED';
    default:
      return 'FAILED';
  }
}

/**
 * Annotations pushed while a test runs surface on the result in current
 * Playwright versions and on the test case in older ones.
 */
function annotationsOf(test: TestCase, result: TestResult): { type: string; description?: string }[] {
  const fromResult = (result as { annotations?: { type: string; description?: string }[] }).annotations;
  return fromResult ?? test.annotations;
}

function providesOf(test: TestCase, result: TestResult): Record<string, string> {
  const provides: Record<string, string> = {};
  for (const a of annotationsOf(test, result)) {
    if (a.type.startsWith(PROVIDE_ANNOTATION_PREFIX)) {
      provides[a.type.slice(PROVIDE_ANNOTATION_PREFIX.length)] = a.description ?? '';
    }
  }
  return provides;
}

function artifactsOf(result: TestResult): PlaydagArtifact[] {
  const artifacts: PlaydagArtifact[] = [];
  for (const att of result.attachments) {
    if (!att.path) continue;
    const type = ARTIFACT_TYPES.find((t) => att.name.includes(t)) ?? 'log';
    artifacts.push({ type, path: att.path });
  }
  return artifacts;
}

/**
 * Reports one node's outcome to the orchestrator in the playdag protocol.
 *
 * The orchestrator runs a single node per Playwright process, so exactly one
 * test is expected to match. The response goes to the file named by
 * PLAYDAG_RESULT, keeping it clear of everything Playwright prints on stdout.
 */
export default class PlaydagReporter implements Reporter {
  private response: PlaydagResponse = {
    schema_version: SCHEMA_VERSION,
    status: 'FAILED',
    error: 'playdag: no test matched the requested node',
  };
  private seen = 0;

  onTestEnd(test: TestCase, result: TestResult): void {
    this.seen += 1;
    if (this.seen > 1) {
      this.response = {
        schema_version: SCHEMA_VERSION,
        status: 'FAILED',
        error: `playdag: ${this.seen} tests matched one node; an @id: tag must select exactly one test`,
      };
      return;
    }

    this.response = {
      schema_version: SCHEMA_VERSION,
      status: toStatus(result),
      provides: providesOf(test, result),
      error: result.error?.message ?? '',
      artifacts: artifactsOf(result),
    };
  }

  onEnd(): void {
    const path = process.env[RESULT_ENV];
    const payload = JSON.stringify(this.response);
    if (!path) {
      process.stdout.write(payload + '\n');
      return;
    }
    writeFileSync(path, payload);
  }
}
