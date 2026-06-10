import type { Metadata } from 'next';

import { CodeBlock } from '@/app/components/ui/CodeBlock';
import {
  DocsCallout,
  DocsH2,
  DocsH3,
  DocsLinkInline,
  DocsList,
  DocsListItem,
  DocsP,
  DocsPage,
  InlineCode,
} from '../components/DocsPage';

export const metadata: Metadata = {
  title: 'Autonomous /goal Mode',
  description:
    'How the /goal slash command drives bounded autonomous iteration in BharatCode: set a goal, run a run-observe-continue loop until the goal is met or the iteration cap is hit, then stop or clear it.',
};

const SET_GOAL = `/goal Make \`go test ./...\` pass. Several tests in the auth package are failing after the session-token refactor. Fix the implementation, not the tests.`;

const RUN_GOAL = `/goal run`;

const STOP_GOAL = `/goal stop`;

const CLEAR_GOAL = `/goal clear`;

// Illustrative only — the exact wording, ordering, and styling of the
// in-TUI progress display are not specified here and may differ in the app.
const TUI_PROGRESS = `goal  Make \`go test ./...\` pass (auth package).
running… iteration 3

  iter 1  read auth/session_test.go, auth/session.go
  iter 2  edit auth/session.go — refresh token TTL
          bash: go test ./auth/  →  2 failing
  iter 3  edit auth/session.go — clock-skew window
          bash: go test ./auth/  →  running

press /goal stop to halt · esc to interrupt the current step`;

const GOOD_GOAL = `/goal Add pagination to the GET /api/orders endpoint: accept ?page and ?page_size query params (page_size capped at 100), return an X-Total-Count header, and cover it with a table test in orders_handler_test.go. Done when go test ./api/... passes and go vet ./... is clean.`;

const VAGUE_GOAL = `/goal Improve the orders API.`;

export default function GoalPage() {
  return (
    <DocsPage
      eyebrow="Usage"
      title="Autonomous /goal Mode"
      lede={
        <>
          <InlineCode>/goal</InlineCode> turns a single instruction into a
          bounded autonomous run. You state an objective, start the loop, and
          BharatCode works toward it on its own — taking an action, observing
          the result, and deciding what to do next — until the goal is met or it
          reaches a fixed iteration cap.
        </>
      }
      prev={{ href: '/docs/tools', label: 'Built-in Tools' }}
      next={{ href: '/docs/sessions', label: 'Sessions & Fork' }}
    >
      <DocsP>
        Most of the time you drive BharatCode turn by turn: you send a prompt,
        it responds, you read the result and send the next prompt.{' '}
        <InlineCode>/goal</InlineCode> hands that loop to the agent. You describe
        the end state you want, and the agent iterates toward it without waiting
        for you to prompt each step — it keeps going until it judges the goal
        complete or it hits a built-in limit that stops it from running forever.
      </DocsP>

      <DocsP>
        <InlineCode>/goal</InlineCode> is a slash command you run inside the TUI.
        It has four operations: set the goal, run the loop, stop a run in
        progress, and clear the goal entirely.
      </DocsP>

      <DocsH2 id="lifecycle">The /goal lifecycle</DocsH2>
      <DocsP>
        A goal moves through a simple lifecycle — you set it, run it, and either
        let it finish, stop it mid-run, or clear it to start over.
      </DocsP>
      <DocsList>
        <DocsListItem>
          <InlineCode>/goal &lt;text&gt;</InlineCode> — <strong className="font-semibold text-fg">set</strong>{' '}
          the goal. The text after the command becomes the objective for the
          session. Setting a goal does not start work; it records what you want
          so you can review or refine it first.
        </DocsListItem>
        <DocsListItem>
          <InlineCode>/goal run</InlineCode> — <strong className="font-semibold text-fg">run</strong>{' '}
          the loop. The agent begins iterating toward the goal and keeps going on
          its own until the goal is met or the iteration cap is reached.
        </DocsListItem>
        <DocsListItem>
          <InlineCode>/goal stop</InlineCode> — <strong className="font-semibold text-fg">halt</strong>{' '}
          a run that is in progress. The loop ends after the current step; the
          goal itself stays set, so you can adjust course and run again.
        </DocsListItem>
        <DocsListItem>
          <InlineCode>/goal clear</InlineCode> — <strong className="font-semibold text-fg">reset</strong>.
          The goal is removed and the session returns to ordinary turn-by-turn
          use.
        </DocsListItem>
      </DocsList>

      <DocsH2 id="set">Setting a goal</DocsH2>
      <DocsP>
        Set a goal by typing <InlineCode>/goal</InlineCode> followed by a plain
        description of the end state you want. Treat it like a task you would
        hand a capable teammate: say what &ldquo;done&rdquo; looks like, and name
        any constraints that matter.
      </DocsP>
      <CodeBlock language="text" label="set the goal" code={SET_GOAL} />
      <DocsP>
        Nothing runs yet. The goal is now attached to the session and you can
        edit it (just run <InlineCode>/goal</InlineCode> again with new text) or
        kick it off when you are ready.
      </DocsP>
      <DocsCallout tone="tip" title="Set first, run second">
        Keeping &ldquo;set&rdquo; and &ldquo;run&rdquo; separate is deliberate:
        it gives you a beat to read your own goal back before the agent starts
        acting on it. A goal that is precise on the screen is far more likely to
        produce the result you want.
      </DocsCallout>

      <DocsH2 id="run-loop">Running the loop</DocsH2>
      <DocsP>
        <InlineCode>/goal run</InlineCode> starts the autonomous loop. Each
        iteration follows the same rhythm:
      </DocsP>
      <DocsList ordered>
        <DocsListItem>
          <strong className="font-semibold text-fg">Run</strong> — the agent
          takes the next concrete action toward the goal, using the same{' '}
          <DocsLinkInline href="/docs/tools">built-in tools</DocsLinkInline> it
          uses in normal turns: reading and editing files, running commands with{' '}
          <InlineCode>bash</InlineCode>, searching the codebase, and so on.
        </DocsListItem>
        <DocsListItem>
          <strong className="font-semibold text-fg">Observe</strong> — it reads
          the result of that action: command output, test results, diagnostics,
          file contents. This is the feedback that tells it whether it is closer
          to the goal.
        </DocsListItem>
        <DocsListItem>
          <strong className="font-semibold text-fg">Continue</strong> — it
          decides whether the goal is now satisfied. If so, the loop ends. If
          not, it plans the next action and starts another iteration.
        </DocsListItem>
      </DocsList>
      <DocsP>
        The loop ends on its own in one of two ways:{' '}
        <strong className="font-semibold text-fg">
          the agent judges the goal met
        </strong>
        , or <strong className="font-semibold text-fg">it reaches the
        iteration cap</strong>. Either way it stops and hands control back to
        you with a summary of what it did.
      </DocsP>
      <CodeBlock language="text" label="start the loop" code={RUN_GOAL} />

      <DocsH2 id="iteration-cap">The iteration cap</DocsH2>
      <DocsP>
        Autonomy without a limit is a runaway. To keep a run bounded, every{' '}
        <InlineCode>/goal run</InlineCode> is governed by an{' '}
        <strong className="font-semibold text-fg">iteration cap</strong> — a
        fixed maximum number of run-observe-continue cycles. When the loop
        reaches that cap it stops, even if the goal is not yet complete. This is
        the safety rail that guarantees a goal run terminates rather than looping
        indefinitely.
      </DocsP>
      <DocsP>
        Hitting the cap is a normal outcome, not an error. If the agent runs out
        of iterations before finishing, it reports where it got to so you can
        decide what to do next: read its progress, refine the goal to be more
        specific, and run again — the work it already did (edited files, passing
        tests) stays in place — or take over manually for the last stretch.
      </DocsP>
      <DocsCallout tone="note" title="Two independent bounds">
        The iteration cap bounds how many <em>steps</em> a run can take. If you
        have set an INR{' '}
        <DocsLinkInline href="/docs/permissions">budget</DocsLinkInline>, that
        acts as a second, independent bound on <em>spend</em> — a run can stop
        because it has done enough iterations or because it would exceed your
        monthly budget gate, whichever comes first.
      </DocsCallout>

      <DocsH2 id="progress">Watching progress in the TUI</DocsH2>
      <DocsP>
        A goal run is not a black box. While the loop is running, the TUI shows
        the goal you set, that a run is in progress, and the iterations as they
        happen — each action the agent takes and the result it observes. You can
        watch it read files, make edits, and run commands in real time, and see
        the moment it decides the goal is met or that it has hit the cap.
      </DocsP>
      <CodeBlock
        language="text"
        label="goal run in progress (illustrative)"
        code={TUI_PROGRESS}
      />
      <DocsP>
        Because the run is visible step by step, you are never locked out: if it
        starts heading the wrong way you can interrupt and{' '}
        <InlineCode>/goal stop</InlineCode> without waiting for it to exhaust the
        cap.
      </DocsP>
      <DocsCallout tone="note" title="Illustrative output">
        The block above is a sketch of the kind of information a run surfaces —
        the goal, the current iteration, and what happened in each one. Exact
        wording and layout are part of the TUI and may differ from what you see.
      </DocsCallout>

      <DocsH2 id="stop-clear">Stopping and clearing</DocsH2>
      <DocsP>
        <InlineCode>/goal stop</InlineCode> halts a run that is in progress. The
        loop ends after the current step rather than tearing work off
        mid-edit, and the goal stays set — so you can tweak your instruction and{' '}
        <InlineCode>/goal run</InlineCode> again from where things stand.
      </DocsP>
      <CodeBlock language="text" label="halt a run in progress" code={STOP_GOAL} />
      <DocsP>
        <InlineCode>/goal clear</InlineCode> removes the goal completely and
        returns the session to ordinary turn-by-turn use. Reach for it when the
        goal is finished, or when you want to start fresh with a different
        objective rather than editing the current one.
      </DocsP>
      <CodeBlock language="text" label="reset the goal" code={CLEAR_GOAL} />

      <DocsH2 id="approval-modes">Goals and approval modes</DocsH2>
      <DocsP>
        An autonomous loop works best when it is not stopping to ask permission
        on every step. How a goal run behaves therefore depends on your current{' '}
        <DocsLinkInline href="/docs/permissions">approval mode</DocsLinkInline>:
      </DocsP>
      <DocsList>
        <DocsListItem>
          In <strong className="font-semibold text-fg">read-only</strong> mode
          the agent can investigate freely but cannot make changes, so a goal
          that requires edits or commands will keep bumping into the permission
          wall.
        </DocsListItem>
        <DocsListItem>
          In <strong className="font-semibold text-fg">auto</strong> mode it can
          proceed through the actions covered by that mode without prompting you,
          which lets the loop actually flow.
        </DocsListItem>
        <DocsListItem>
          In <strong className="font-semibold text-fg">full</strong> mode it can
          take any action without per-step approval — the most hands-off setting
          for an unattended run.
        </DocsListItem>
      </DocsList>
      <DocsCallout tone="warn" title="Match autonomy to trust">
        A goal run is as autonomous as the mode you put it in. Looser approval
        modes let the loop move faster but also let it act with less
        confirmation. Use the mode you are comfortable handing the wheel to for
        this particular task — the iteration cap and your budget gate are still
        there as backstops.
      </DocsCallout>

      <DocsH2 id="writing-goals">Writing good goals</DocsH2>
      <DocsP>
        The single biggest factor in how well a goal run goes is the goal you
        write. The agent can only iterate toward an objective it can understand
        and recognize when it has reached. A few habits make a large difference.
      </DocsP>

      <DocsH3 id="tip-done">Define &ldquo;done&rdquo; concretely</DocsH3>
      <DocsP>
        Give the loop a finish line it can check. A goal that names a verifiable
        condition — a command that should pass, a behavior that should exist — is
        one the agent can confirm for itself each iteration. Vague goals never
        clearly resolve, so they tend to drift until they hit the cap.
      </DocsP>
      <CodeBlock language="text" label="vague — hard to finish" code={VAGUE_GOAL} />
      <CodeBlock language="text" label="concrete — has a finish line" code={GOOD_GOAL} />

      <DocsH3 id="tip-checks">Point at a check the agent can run</DocsH3>
      <DocsP>
        Whenever you can, tie &ldquo;done&rdquo; to something the agent can
        verify with a tool — <InlineCode>go test ./...</InlineCode>,{' '}
        <InlineCode>go vet ./...</InlineCode>, a build command, or your project&apos;s
        test command. A self-checkable condition turns the observe step into real
        feedback and keeps the loop honest about whether it is actually done.
      </DocsP>

      <DocsH3 id="tip-scope">Scope it so it fits the cap</DocsH3>
      <DocsP>
        Because each run is bounded by the iteration cap, a goal that needs a
        hundred steps is unlikely to finish in one run. Prefer a sharply scoped
        goal — one bug, one endpoint, one refactor — over a sprawling one. If a
        large task is naturally several pieces, run them as a sequence of focused
        goals rather than one open-ended objective.
      </DocsP>

      <DocsH3 id="tip-constraints">State constraints and boundaries</DocsH3>
      <DocsP>
        Tell the loop what <em>not</em> to do as clearly as what to do:
        &ldquo;fix the implementation, not the tests,&rdquo; &ldquo;don&apos;t
        touch the public API,&rdquo; &ldquo;stay inside the{' '}
        <InlineCode>api/</InlineCode> package.&rdquo; Constraints keep an
        autonomous agent from taking shortcuts that technically satisfy the goal
        but aren&apos;t what you meant. Durable, project-wide boundaries belong in
        your{' '}
        <DocsLinkInline href="/docs/agents-md">
          AGENTS.md
        </DocsLinkInline>{' '}
        instead, so every goal inherits them automatically.
      </DocsP>

      <DocsCallout tone="tip" title="Refine, don't restart">
        If a run hits the cap or wanders, you rarely need to start from scratch.
        Read what it did, sharpen the goal with the next{' '}
        <InlineCode>/goal &lt;text&gt;</InlineCode>, and{' '}
        <InlineCode>/goal run</InlineCode> again. The progress already on disk
        carries forward — each run picks up from the current state of your code.
      </DocsCallout>
    </DocsPage>
  );
}
