(() => {
  try {
    const value = String(localStorage.getItem("tanpopo.uiTheme") || "tanpopo").trim().toLowerCase();
    document.documentElement.dataset.theme = ["tanpopo", "ocean", "sakura", "wisteria"].includes(value)
      ? value
      : "tanpopo";
  } catch (_error) {
    document.documentElement.dataset.theme = "tanpopo";
  }
})();
