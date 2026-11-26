# rag-ingest

An agent that indexes meeting notes from a Drive tool into the governed memory
store. It makes no chat call: it reads notes through the Drive tool, embeds
them through the embeddings model, and writes them to the memory tool.

## What it does

On each run the agent runs a fixed Drive search for `sync`; the run input is
ignored. It reads each matching note and takes the meeting date from the note
body's first line, which must read `Meeting notes - YYYY-MM-DD`. It splits the
body into chunks, one per paragraph (a paragraph that is too long is split at
sentence boundaries), and embeds each chunk through the logical embeddings
model `embed`. It then stores each chunk in the `memory` tool's collection
`notes` with the id `<documentId>#<chunkIndex>` and the metadata `source`,
`chunk_index`, and `date`.

The result is `indexed N chunks (M undated)`, where N is the number of chunks
stored and M is the number of those chunks whose note had no parseable date.
Re-running overwrites the same ids, so repeated runs do not duplicate chunks.

## Operator setup

Define these in your gateway configuration:

- A tool resource named `memory`, an MCP tool server, whose `read_only_tools`
  does not include `memory_upsert`.
- A tool resource named `drive` that exposes `search` and `readGoogleDoc`.
- An embeddings model named `embed`.

Because `memory_upsert` writes to your store, the agent holds a read-write tool
grant. A run is therefore confirmed before it proceeds: it warns that it writes
to your store and that this cannot be undone. Pass `--yes` to run it
non-interactively.

## Enroll

```
agenthof enroll https://raw.githubusercontent.com/agenthof/hub/<tag>/agents/rag-ingest/card.json ...
```

Replace `<tag>` with a release tag of this repository, and see
`agenthof enroll --help` for the remaining arguments.

## Limits

- The card requests no chat model and the agent makes no chat call. If your
  gateway sets a default model, the chat door falls back to it, so the
  enrolled agent's disclosure may list that default model as `not requested`.
  The agent never calls it.
- The Drive search is expected to return one hit per line as
  `<documentId> <title>`, the shape the reference Drive tool returns. A Drive
  server that returns a different shape may not be parsed, and a hit that
  cannot be read as a document ends the run, after any chunks already stored.
- There is no delete. If a note is shortened or removed, its old chunks remain
  in the store and can still surface in later retrieval.

The program is started by the engine's supervisor with `-socket <path>` and is
not meant to be run by hand.
