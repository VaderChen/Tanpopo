const form = document.getElementById("loginForm");
const button = document.getElementById("loginButton");
const message = document.getElementById("loginMessage");
const account = document.getElementById("account");
const password = document.getElementById("password");
const rememberMe = document.getElementById("rememberMe");

form.addEventListener("submit", async (event) => {
  event.preventDefault();
  button.disabled = true;
  message.textContent = "";
  try {
    const response = await fetch("/api/login", {
      method: "POST",
      headers: { "Content-Type": "application/json", "Accept": "application/json" },
      credentials: "same-origin",
      body: JSON.stringify({
        account: account.value,
        password: password.value,
        remember_me: rememberMe.checked
      })
    });
    const payload = await response.json();
    if (!response.ok) {
      throw new Error(payload?.error?.message || "登入失敗");
    }
    location.replace("/main.html");
  } catch (error) {
    message.textContent = error.message || "登入失敗";
  } finally {
    button.disabled = false;
  }
});
