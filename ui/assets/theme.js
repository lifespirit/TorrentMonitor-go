(function () {
  const storageKey = "torrentmonitor-theme"
  const media = window.matchMedia("(prefers-color-scheme: dark)")

  function getPreference() {
    const value = localStorage.getItem(storageKey)
    return ["system", "light", "dark"].includes(value) ? value : "system"
  }

  function apply(preference = getPreference()) {
    const resolved = preference === "system" ? (media.matches ? "dark" : "light") : preference
    document.documentElement.dataset.theme = resolved
    document.documentElement.style.colorScheme = resolved
  }

  function setPreference(preference) {
    const value = ["system", "light", "dark"].includes(preference) ? preference : "system"
    localStorage.setItem(storageKey, value)
    apply(value)
  }

  media.addEventListener("change", () => {
    if (getPreference() === "system") apply("system")
  })

  window.TMTheme = {getPreference, setPreference, apply}
  apply()
})()
