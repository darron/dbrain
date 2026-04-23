<script>
  export let question = "";
  export let state = "idle";
  export let onAsk = () => {};
</script>

<section class="panel stack focus-panel">
  <div class="panel-header">
    <div>
      <p class="panel-kicker">Ask</p>
      <h2>Pull together evidence around a subject.</h2>
    </div>
  </div>

  <p class="message muted">
    Best when you want a small pack of related notes and linked sources without manually searching
    through each one.
  </p>

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
      {state === "loading" ? "Retrieving..." : "Ask"}
    </button>
  </form>
</section>
