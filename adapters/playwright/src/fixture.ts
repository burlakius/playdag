import { test as base } from '@playwright/test';
import type { TestInfo } from '@playwright/test';
import { ID_ANNOTATION, PROVIDE_ANNOTATION_PREFIX } from './annotations';
import { requestFor } from './protocol';
import type { PlaydagRequest } from './protocol';

/**
 * The context a test reads from and writes to. Values produced by an earlier
 * node arrive through `get`; values this node promised are handed back through
 * `provide`, which is how a login test passes its session to everything after it.
 */
export interface PlaydagContext {
  /** Node identifier, or undefined when the test runs outside playdag. */
  readonly testId?: string;
  /** 1-based attempt number; greater than 1 means this is a retry. */
  readonly attempt: number;
  /** Value for a key this node requires. Throws when it was not provided. */
  get(key: string): string;
  /** Value for a key, or undefined when it is absent. */
  tryGet(key: string): string | undefined;
  /** Publishes a value for the nodes that depend on this one. */
  provide(key: string, value: string | number | boolean): void;
}

export interface PlaydagFixtures {
  playdag: PlaydagContext;
}

function contextOf(req: PlaydagRequest | null): Record<string, string> {
  return req?.context ?? {};
}

/** The node this test declares itself to be, which is how it finds its own request. */
function nodeIdOf(testInfo: TestInfo): string | undefined {
  return testInfo.annotations.find((a) => a.type === ID_ANNOTATION)?.description;
}

/**
 * `test` extended with the playdag fixture. Import it instead of @playwright/test.
 *
 * The fixture works without an orchestrator too: `get` then simply finds nothing,
 * so a spec can still be run and debugged with plain `npx playwright test`.
 */
export const test = base.extend<PlaydagFixtures>({
  playdag: async ({}, use, testInfo) => {
    const req = requestFor(nodeIdOf(testInfo));
    const context = contextOf(req);

    const ctx: PlaydagContext = {
      testId: req?.test_id,
      attempt: req?.attempt ?? 1,

      get(key: string): string {
        const value = context[key];
        if (value === undefined) {
          const known = Object.keys(context).join(', ') || 'nothing';
          throw new Error(
            `playdag: required key "${key}" is not in the context (available: ${known}). ` +
              `Declare it in the test's requires and make sure a node provides it.`,
          );
        }
        return value;
      },

      tryGet(key: string): string | undefined {
        return context[key];
      },

      provide(key: string, value: string | number | boolean): void {
        // Annotations are the channel back to the reporter, which is what turns
        // them into the node's provides in the protocol response.
        testInfo.annotations.push({
          type: PROVIDE_ANNOTATION_PREFIX + key,
          description: String(value),
        });
      },
    };

    await use(ctx);
  },
});

export { expect } from '@playwright/test';
