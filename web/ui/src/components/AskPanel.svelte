<script>
  import ResultList from "./ResultList.svelte";

  export let question = "";
  export let state = "idle";
  export let error = "";
  export let response = { question: "", answer: "", evidence: [] };
  export let selectedLookup = "";
  export let onAsk = () => {};
  export let onSelect = () => {};
</script>

<section class="panel stack">
  <div class="panel-header">
    <div>
      <p class="panel-kicker">Ask</p>
      <h2>Retrieve evidence only</h2>
    </div>
  </div>

  <form class="form" on:submit|preventDefault={onAsk}>
    <label>
      <span>Question</span>
      <textarea
        bind:value={question}
        placeholder="What do I already have on agent memory?"
        rows="4"
      ></textarea>
    </label>
    <button type="submit" disabled={state === "loading"}>
      {state === "loading" ? "Retrieving..." : "Retrieve"}
    </button>
  </form>

  {#if error}
    <p class="message error">{error}</p>
  {/if}

  {#if state === "ready"}
    <div class="ask-response stack-sm">
      <p class="message muted">
        Evidence only. This avoids synthesized answers while source work is still running.
      </p>

      <ResultList
        items={response.evidence || []}
        selectedLookup={selectedLookup}
        onSelect={onSelect}
        emptyMessage="No evidence matched that question yet."
      />
    </div>
  {/if}
</section>
