export function confirmShareDeletion(confirm, share) {
  if (typeof confirm !== "function") return false;
  const title = String(share?.title || "Shared dbrain answer").trim();
  return confirm(`Delete public share “${title}”? Its public URL will stop working immediately.`) === true;
}

export function createShareListGeneration() {
  let current = 0;
  return {
    begin() {
      current += 1;
      return current;
    },
    invalidate() {
      current += 1;
    },
    isCurrent(generation) {
      return generation === current;
    }
  };
}

export function nextShareDeleteState(current, slug, pending, error = "") {
  return {
    ...current,
    [slug]: {
      pending: pending === true,
      error: String(error || "")
    }
  };
}

export function removeDeletedShareReferences(shares, chatShareByTurn, slug) {
  const remainingShares = (Array.isArray(shares) ? shares : []).filter((share) => share?.slug !== slug);
  const remainingByTurn = Object.fromEntries(
    Object.entries(chatShareByTurn || {}).filter(([, share]) => share?.slug !== slug)
  );
  return {
    shares: remainingShares,
    chatShareByTurn: remainingByTurn
  };
}
