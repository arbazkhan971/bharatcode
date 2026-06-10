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
  title: 'Sessions & Fork',
  description:
    'BharatCode persists every conversation in a local SQLite database. Resume the latest with bharatcode --continue, pick a past one with /sessions, branch an exploration with /fork, and list them all with bharatcode sessions.',
};

export default function SessionsPage() {
  return (
    <DocsPage
      eyebrow="Usage"
      title="Sessions & Fork"
      lede={
        <>
          Every conversation with BharatCode is a <em>session</em>, and every
          session is saved automatically to a local database on your machine.
          You never lose your place: resume where you left off, jump back into an
          older conversation, or branch a new line of exploration from a
          known-good point &mdash; all without losing the original.
        </>
      }
      prev={{ href: '/docs/goal', label: '/goal Autonomous Mode' }}
      next={{ href: '/docs/permissions', label: 'Permissions' }}
    >
      <DocsH2 id="persistent-sessions">Persistent sessions</DocsH2>
      <DocsP>
        Sessions in BharatCode are persistent by default. The moment you start
        talking to the agent &mdash; in the interactive TUI or via{' '}
        <InlineCode>bharatcode run</InlineCode> &mdash; a session is created and
        every turn of the conversation is written to a local SQLite database as
        it happens. There is no separate &ldquo;save&rdquo; step to remember: the
        history, the messages, the tool calls, and the edits are all recorded so
        that nothing is lost if you close the terminal or your machine restarts.
      </DocsP>
      <DocsP>
        Because the store is SQLite, your sessions live entirely on your own
        machine as a single self-contained file &mdash; no server, no account,
        and no data leaving your laptop. That is the same data-stays-local
        principle that runs through the rest of BharatCode.
      </DocsP>
      <DocsCallout tone="note" title="Local & private">
        Session history is stored locally in SQLite. It is yours: you can back it
        up, copy it between machines, or delete it like any other file. BharatCode
        does not sync your conversations anywhere.
      </DocsCallout>

      <DocsH2 id="resume-latest">Resume the latest session</DocsH2>
      <DocsP>
        The fastest way to pick up where you left off is to launch with{' '}
        <InlineCode>--continue</InlineCode>. It reopens the most recently active
        session in the current directory and drops you straight back into the
        conversation, with its full history intact.
      </DocsP>
      <CodeBlock
        language="bash"
        label="terminal"
        prompt
        code={'bharatcode --continue'}
      />
      <DocsP>
        This is the right command for the common case: you were working on
        something, stepped away, and want to keep going. No need to find or name
        the session &mdash; <InlineCode>--continue</InlineCode> always takes the
        latest one.
      </DocsP>

      <DocsH2 id="pick-a-past-session">Pick a past session</DocsH2>
      <DocsP>
        When the latest session is not the one you want, use{' '}
        <InlineCode>/sessions</InlineCode> from inside the TUI. It opens a picker
        listing your saved sessions so you can browse and restore any one of
        them, continuing that conversation exactly where it ended.
      </DocsP>
      <CodeBlock
        language="text"
        label="bharatcode TUI"
        code={'/sessions'}
      />
      <DocsP>
        Think of <InlineCode>/sessions</InlineCode> as the deliberate
        counterpart to <InlineCode>--continue</InlineCode>: instead of always
        taking the most recent session, you choose <em>which</em> one to reopen.
        It is also where you go to revisit an exploration you forked off earlier.
      </DocsP>
      <DocsCallout tone="tip" title="From the shell vs. in the TUI">
        Use <InlineCode>bharatcode --continue</InlineCode> when you know you want
        the latest session and want to skip the picker entirely. Use{' '}
        <InlineCode>/sessions</InlineCode> when you are already in the TUI, or
        when you want to choose an older conversation rather than the most recent
        one.
      </DocsCallout>

      <DocsH2 id="fork">Branch an exploration with /fork</DocsH2>
      <DocsP>
        Forking is how you try something without putting your current
        conversation at risk. Running <InlineCode>/fork</InlineCode> creates a{' '}
        <em>new</em> session that copies the history of the current one up to
        this point, and switches you into it. The original session is left
        completely untouched &mdash; you can always go back to it with{' '}
        <InlineCode>/sessions</InlineCode>.
      </DocsP>
      <CodeBlock language="text" label="bharatcode TUI" code={'/fork'} />
      <DocsP>
        Because the fork starts as an exact copy, the agent keeps all the context
        it had built up &mdash; the files it has read, the decisions made so far
        &mdash; but anything you do next happens only in the branch. This makes{' '}
        <InlineCode>/fork</InlineCode> ideal for:
      </DocsP>
      <DocsList>
        <DocsListItem>
          Trying a riskier or more aggressive change from a known-good point,
          with a clean way back if it does not pan out.
        </DocsListItem>
        <DocsListItem>
          Exploring two approaches in parallel &mdash; fork once per approach and
          compare the results.
        </DocsListItem>
        <DocsListItem>
          Asking a tangential question without polluting the main thread, then
          returning to the original to keep going.
        </DocsListItem>
      </DocsList>
      <DocsCallout tone="note" title="Fork vs. clear">
        <InlineCode>/fork</InlineCode> keeps the conversation history and copies
        it into a new branch, so the agent retains its context.{' '}
        <DocsLinkInline href="/docs/commands">
          <InlineCode>/clear</InlineCode>
        </DocsLinkInline>{' '}
        is different: it starts a fresh context in place, dropping the prior
        conversation rather than branching from it.
      </DocsCallout>

      <DocsH2 id="list-sessions">List your sessions</DocsH2>
      <DocsP>
        To see your saved sessions from the shell &mdash; without opening the TUI
        &mdash; run the <InlineCode>sessions</InlineCode> subcommand:
      </DocsP>
      <CodeBlock
        language="bash"
        label="terminal"
        prompt
        code={'bharatcode sessions'}
      />
      <DocsP>
        This lists the sessions BharatCode has on record, which is handy for
        getting an overview, scripting, or confirming that a session you expect
        is actually there. To reopen the most recent one, follow up with{' '}
        <InlineCode>bharatcode --continue</InlineCode>; to choose a specific one
        interactively, launch the TUI and use <InlineCode>/sessions</InlineCode>.
      </DocsP>

      <DocsH2 id="where-the-db-lives">Where the database lives</DocsH2>
      <DocsP>
        All of your sessions live in a single SQLite database file in
        BharatCode&apos;s data directory, following the XDG base-directory spec:
      </DocsP>
      <CodeBlock
        language="text"
        label="session database"
        code={`~/.local/share/bharatcode/bharatcode.db

# or, when $XDG_DATA_HOME is set:
$XDG_DATA_HOME/bharatcode/bharatcode.db`}
      />
      <DocsP>
        Note that this is your <em>data</em> directory
        (<InlineCode>~/.local/share/</InlineCode>), which is separate from your{' '}
        <em>config</em> directory at{' '}
        <DocsLinkInline href="/docs/configuration">
          <InlineCode>~/.config/bharatcode/</InlineCode>
        </DocsLinkInline>{' '}
        where <InlineCode>config.json</InlineCode> lives. Sessions, messages, the
        per-session file-change history, and the INR cost ledger all share this
        one database, so it is the complete record of everything BharatCode has
        done on your machine. It is opened with{' '}
        <InlineCode>modernc.org/sqlite</InlineCode>, a pure-Go driver, which is
        part of why BharatCode ships CGO-free.
      </DocsP>
      <DocsP>
        If you want to confirm the exact location on your system &mdash; paths can
        differ by OS and by environment &mdash; run the doctor check, which
        reports where BharatCode is reading its state from:
      </DocsP>
      <CodeBlock
        language="bash"
        label="terminal"
        prompt
        code={'bharatcode doctor'}
      />
      <DocsCallout tone="warn" title="Treat it like real data">
        <InlineCode>bharatcode.db</InlineCode> is the record of every
        conversation. Deleting it removes that history permanently, so if you
        rely on being able to resume older sessions, include{' '}
        <InlineCode>~/.local/share/bharatcode/</InlineCode> in your backups. Since
        it is one self-contained file, copying it is all it takes to move your
        sessions to another machine.
      </DocsCallout>

      <DocsH2 id="see-also">See also</DocsH2>
      <DocsList>
        <DocsListItem>
          <DocsLinkInline href="/docs/commands">
            TUI &amp; Slash Commands
          </DocsLinkInline>{' '}
          &mdash; the full list of in-TUI commands, including{' '}
          <InlineCode>/sessions</InlineCode> and <InlineCode>/fork</InlineCode>.
        </DocsListItem>
        <DocsListItem>
          <DocsLinkInline href="/docs/configuration">
            Config files
          </DocsLinkInline>{' '}
          &mdash; the <InlineCode>~/.config/bharatcode/</InlineCode> config
          directory, distinct from the data directory your sessions live in.
        </DocsListItem>
        <DocsListItem>
          <DocsLinkInline href="/docs/cli">CLI Reference</DocsLinkInline>{' '}
          &mdash; <InlineCode>sessions</InlineCode>,{' '}
          <InlineCode>--continue</InlineCode>, and the other subcommands and
          flags.
        </DocsListItem>
      </DocsList>
    </DocsPage>
  );
}
