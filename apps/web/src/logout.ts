import "./style.css";
import "./theme";
import { request } from "./api";
import { configureTasklyLinks, tasklyLoginURL } from "./config";
import { element } from "./dom";

const error = element("error", HTMLParagraphElement);
const retry = element("retry", HTMLButtonElement);

configureTasklyLinks();

async function finishLogout(): Promise<void> {
  retry.hidden = true;
  error.textContent = "";
  try {
    const response = await request("/auth/logout", {});
    if (!response.ok) throw new Error("logout failed");
    location.replace(tasklyLoginURL);
  } catch {
    error.textContent = "ログアウトを完了できませんでした。再試行してください。";
    retry.hidden = false;
  }
}

retry.addEventListener("click", finishLogout);
void finishLogout();
