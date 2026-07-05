import assert from "node:assert/strict";
import test from "node:test";

import {
  buildChatTraceContinuity,
  buildChatRetrievalQuestion,
  collectPriorEvidence,
  mergeEvidenceRows,
  mergeResearchPackForChat,
  normalizeStoredChatSession
} from "./chat.js";

test("buildChatRetrievalQuestion includes prior questions and compact evidence hints, not prior answers or source keys", () => {
  const turns = [
    {
      question: "What do I know about Hermes memory?",
      answer: "DO_NOT_INCLUDE_THIS_MODEL_ANSWER",
      research_pack: {
        evidence: [
          { source_key: "src:alpha", title: "Alpha" },
          { source_key: "src:beta", title: "Grafana Tanka", user_tags: "jsonnet, configuration-management, yaml-alternative", summary: "Jsonnet-based Kubernetes configuration tool." }
        ]
      }
    }
  ];

  const query = buildChatRetrievalQuestion("What about implementation tradeoffs?", turns, ["src:beta"]);

  assert.match(query, /Current question: What about implementation tradeoffs\?/);
  assert.match(query, /What do I know about Hermes memory\?/);
  assert.doesNotMatch(query, /src:beta/);
  assert.match(query, /Grafana Tanka/);
  assert.doesNotMatch(query, /configuration-management/);
  assert.doesNotMatch(query, /Jsonnet-based Kubernetes configuration tool/);
  assert.doesNotMatch(query, /DO_NOT_INCLUDE_THIS_MODEL_ANSWER/);
});

test("buildChatRetrievalQuestion does not turn prior summaries and tags into broad follow-up searches", () => {
  const turns = [
    {
      question: "Can you tell me about the father in Calgary that killed his two children?",
      research_pack: {
        evidence: [
          {
            source_key: "src:calgary",
            title: "Calgary father facing 2 counts of first-degree murder in deaths of two children",
            source_type: "web",
            user_tags: "social-issues, canadian-crime, publication-ban",
            summary: "Article discusses Calgary police, Eritrean community reactions, family tragedy, domestic violence, and court restrictions."
          }
        ]
      }
    }
  ];

  const query = buildChatRetrievalQuestion("That's the one I was looking for - do you have any other detailed information?", turns, []);

  assert.match(query, /Current question: That's the one/);
  assert.match(query, /father in Calgary/);
  assert.match(query, /Calgary father facing 2 counts/);
  assert.doesNotMatch(query, /social-issues/);
  assert.doesNotMatch(query, /publication-ban/);
  assert.doesNotMatch(query, /Eritrean community/);
  assert.doesNotMatch(query, /src:calgary/);
});

test("buildChatRetrievalQuestion does not pollute standalone title searches with prior questions", () => {
  const turns = [
    {
      question: "Two young children found in an SUV in Calgary.",
      research_pack: {
        evidence: [{ source_key: "x:older", title: "Older unrelated evidence", user_tags: "calgary" }]
      }
    }
  ];

  const query = buildChatRetrievalQuestion("Father charged with killing young son, daughter who were found in vehicle in Calgary", turns, []);

  assert.match(query, /Current question: Father charged/);
  assert.doesNotMatch(query, /Two young children found/);
  assert.doesNotMatch(query, /x:older/);
  assert.doesNotMatch(query, /Older unrelated evidence/);
});

test("buildChatRetrievalQuestion does not anchor corrective follow-ups to prior bad evidence", () => {
  const turns = [
    {
      question: "Can you find the information about the Calgary man that killed his two kids?",
      research_pack: {
        evidence: [{ source_key: "src:wrong", title: "Woman killed by husband in different Calgary case", user_tags: "domestic-homicide" }]
      }
    }
  ];

  const query = buildChatRetrievalQuestion("There is data in the brain about that. The children were found in an SUV.", turns, []);

  assert.match(query, /Current question: There is data/);
  assert.doesNotMatch(query, /Woman killed by husband/);
  assert.doesNotMatch(query, /src:wrong/);
});

test("buildChatRetrievalQuestion suppresses prior evidence for current handle searches", () => {
  const turns = [
    {
      question: "What do I know about NotebookLM?",
      research_pack: {
        evidence: [{ source_key: "src:notebook", title: "NotebookLM maxxing" }]
      }
    }
  ];

  const query = buildChatRetrievalQuestion("Synthesize @Kristof_Poland", turns, []);

  assert.equal(query, "Current question: Synthesize @Kristof_Poland");
  assert.doesNotMatch(query, /Recent user questions/);
  assert.doesNotMatch(query, /Prior evidence titles/);
  assert.doesNotMatch(query, /NotebookLM/);
});

test("buildChatRetrievalQuestion suppresses prior evidence for current underscore aliases", () => {
  const turns = [
    {
      question: "What do I know about NotebookLM?",
      research_pack: {
        evidence: [{ source_key: "src:notebook", title: "NotebookLM maxxing" }]
      }
    }
  ];

  const query = buildChatRetrievalQuestion("Synthesize Kristof_Poland essays", turns, []);

  assert.equal(query, "Current question: Synthesize Kristof_Poland essays");
	assert.doesNotMatch(query, /Recent user questions/);
	assert.doesNotMatch(query, /Prior evidence titles/);
	assert.doesNotMatch(query, /NotebookLM/);
});

test("buildChatRetrievalQuestion does not treat technical underscores as protected anchors", () => {
  const turns = [
    {
      question: "What do I know about NotebookLM?",
      research_pack: {
        evidence: [{ source_key: "src:notebook", title: "NotebookLM maxxing" }]
      }
    }
  ];

  const query = buildChatRetrievalQuestion("Synthesize notes about api_gateway configuration", turns, []);

  assert.match(query, /Current question: Synthesize notes about api_gateway configuration/);
  assert.match(query, /Prior evidence titles/);
  assert.match(query, /NotebookLM/);
});

test("buildChatTraceContinuity carries prior typed anchors only for pronoun followups", () => {
  const anchor = {
    kind: "handle",
    relation: "authored_by",
    raw: "@Kristof_Poland",
    canonical: "kristof_poland"
  };
  const turns = [
    {
      id: "chat:prior",
      question: "Can you synthesize @Kristof_Poland?",
      research_pack: {
        query_plan: { protected_anchors: [anchor] },
        evidence: [{ source_key: "x:kristof-1", title: "Kristof row" }]
      }
    }
  ];

  const continuity = buildChatTraceContinuity("Synthesize those", "Current question: Synthesize those", turns, ["x:kristof-1"]);

  assert.equal(continuity.original_question, "Synthesize those");
  assert.equal(continuity.retrieval_question, "Current question: Synthesize those");
  assert.deepEqual(continuity.prior_question_ids, ["chat:prior"]);
  assert.deepEqual(continuity.pinned_evidence_keys, ["x:kristof-1"]);
  assert.deepEqual(continuity.merged_prior_evidence, ["x:kristof-1"]);
  assert.deepEqual(continuity.continuity_anchors, [anchor]);
});

test("buildChatTraceContinuity replaces stale prior anchors when current turn has an explicit anchor", () => {
  const turns = [
    {
      id: "chat:prior",
      question: "Can you synthesize @Kristof_Poland?",
      research_pack: {
        query_plan: {
          protected_anchors: [{ kind: "handle", relation: "authored_by", raw: "@Kristof_Poland", canonical: "kristof_poland" }]
        },
        evidence: [{ source_key: "x:kristof-1", title: "Kristof row" }]
      }
    }
  ];

  const continuity = buildChatTraceContinuity("Synthesize @Other_Author", "Current question: Synthesize @Other_Author", turns, []);

  assert.equal(continuity.original_question, "Synthesize @Other_Author");
	assert.ok(!("continuity_anchors" in continuity), `unexpected continuity anchors: ${JSON.stringify(continuity)}`);
});

test("buildChatTraceContinuity carries only the most recent anchored turn", () => {
  const older = { kind: "handle", relation: "authored_by", raw: "@Older_Author", canonical: "older_author" };
  const recent = { kind: "handle", relation: "authored_by", raw: "@Kristof_Poland", canonical: "kristof_poland" };
  const turns = [
    {
      id: "chat:older",
      research_pack: {
        query_plan: { protected_anchors: [older] },
        evidence: [{ source_key: "x:older", title: "Older row" }]
      }
    },
    {
      id: "chat:recent",
      research_pack: {
        query_plan: { protected_anchors: [recent] },
        evidence: [{ source_key: "x:kristof-1", title: "Kristof row" }]
      }
    }
  ];

  const continuity = buildChatTraceContinuity("Synthesize those", "Current question: Synthesize those", turns, []);

	assert.deepEqual(continuity.continuity_anchors, [recent]);
	assert.deepEqual(continuity.prior_question_ids, ["chat:older", "chat:recent"]);
});

test("buildChatTraceContinuity keeps prior anchors for pronoun followups with code-like underscores", () => {
  const anchor = {
    kind: "handle",
    relation: "authored_by",
    raw: "@Kristof_Poland",
    canonical: "kristof_poland"
  };
  const turns = [
    {
      id: "chat:prior",
      question: "Can you synthesize @Kristof_Poland?",
      research_pack: {
        query_plan: { protected_anchors: [anchor] },
        evidence: [{ source_key: "x:kristof-1", title: "Kristof row" }]
      }
    }
  ];

  const retrieval = buildChatRetrievalQuestion("Can you update those user_id tokens?", turns, []);
  const continuity = buildChatTraceContinuity("Can you update those user_id tokens?", retrieval, turns, []);

  assert.match(retrieval, /Prior evidence titles/);
  assert.deepEqual(continuity.continuity_anchors, [anchor]);
});

test("mergeEvidenceRows keeps current evidence first and de-duplicates prior evidence", () => {
  const rows = mergeEvidenceRows(
    [
      { source_key: "src:current", title: "Current" },
      { source_key: "src:shared", title: "Current Shared" }
    ],
    [
      { source_key: "src:shared", title: "Old Shared" },
      { source_key: "src:prior", title: "Prior" }
    ],
    5
  );

  assert.deepEqual(rows.map((row) => row.source_key), ["src:current", "src:shared", "src:prior"]);
  assert.equal(rows[1].title, "Current Shared");
});

test("mergeResearchPackForChat appends prior evidence without storing previous answers", () => {
  const currentPack = {
    question: "contextual retrieval query",
    evidence: [{ source_key: "src:new", title: "New" }],
    coverage: { recall_note: "current pack" }
  };
  const turns = [
    {
      question: "Earlier question",
      answer: "DO_NOT_STORE_AS_EVIDENCE",
      research_pack: {
        evidence: [{ source_key: "src:old", title: "Old", summary: "prior summary" }]
      }
    }
  ];

  const merged = mergeResearchPackForChat("Follow-up question", currentPack, turns, [], { maxMergedEvidence: 4 });

  assert.equal(merged.question, "Follow-up question");
  assert.deepEqual(merged.evidence.map((row) => row.source_key), ["src:new", "src:old"]);
  assert.equal(merged.evidence[1].relationship, "prior_chat_context");
  assert.doesNotMatch(JSON.stringify(merged), /DO_NOT_STORE_AS_EVIDENCE/);
});

test("collectPriorEvidence prioritizes pinned evidence before recent fallback", () => {
  const turns = [
    {
      question: "first",
      research_pack: { evidence: [{ source_key: "src:first" }] }
    },
    {
      question: "second",
      research_pack: { evidence: [{ source_key: "src:second" }, { source_key: "src:pinned" }] }
    }
  ];

  const rows = collectPriorEvidence(turns, ["src:pinned"], 3);

  assert.deepEqual(rows.map((row) => row.source_key), ["src:pinned", "src:second", "src:first"]);
});

test("normalizeStoredChatSession filters unusable turns and pins", () => {
  const session = normalizeStoredChatSession({
    turns: [
      { id: "ok", question: "Saved?", status: "verification_failed", done: { trace_path: "research-runs/run-1" }, progress: [{ stage: "verification" }], research_pack: { evidence: [] } },
      { id: "", question: "missing id" },
      { id: "missing-question", question: "" }
    ],
    pinnedEvidenceKeys: ["src:one", "", "src:two"]
  });

  assert.equal(session.turns.length, 1);
  assert.equal(session.turns[0].status, "verification_failed");
  assert.equal(session.turns[0].done.trace_path, "research-runs/run-1");
  assert.deepEqual(session.turns[0].progress, [{ stage: "verification" }]);
  assert.deepEqual(session.pinnedEvidenceKeys, ["src:one", "src:two"]);
});
