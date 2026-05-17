"use strict";

const DEFAULT_BASE_URL = "http://127.0.0.1:8742";
const ext = globalThis.browser || globalThis.chrome;

const form = document.querySelector("#options-form");
const input = document.querySelector("#base-url");
const statusNode = document.querySelector("#status");
const openButton = document.querySelector("#open-dbrain");

document.addEventListener("DOMContentLoaded", () => {
	void loadOptions();
});

form.addEventListener("submit", (event) => {
	event.preventDefault();
	void saveOptions();
});

openButton.addEventListener("click", () => {
	try {
		const baseUrl = normalizeBaseURL(input.value || DEFAULT_BASE_URL);
		void ext.tabs.create({ url: baseUrl });
	} catch (err) {
		showStatus(messageFor(err), "error");
	}
});

async function loadOptions() {
	const config = await ext.storage.sync.get({ baseUrl: DEFAULT_BASE_URL });
	input.value = config.baseUrl || DEFAULT_BASE_URL;
}

async function saveOptions() {
	setDisabled(true);
	try {
		const baseUrl = normalizeBaseURL(input.value);
		const origin = originPattern(baseUrl);
		const granted = await ensureOriginPermission(origin);
		if (!granted) {
			showStatus("Host permission is required before the extension can post links.", "error");
			return;
		}
		await ext.storage.sync.set({ baseUrl });
		input.value = baseUrl;
		showStatus("Saved. Click the toolbar button to save the current page.", "ok");
	} catch (err) {
		showStatus(messageFor(err), "error");
	} finally {
		setDisabled(false);
	}
}

function normalizeBaseURL(raw) {
	const value = String(raw || "").trim();
	if (!value) {
		throw new Error("Enter the dbrain web base URL.");
	}
	const parsed = new URL(value);
	if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
		throw new Error("The dbrain base URL must start with http:// or https://.");
	}
	parsed.pathname = "/";
	parsed.search = "";
	parsed.hash = "";
	return parsed.toString();
}

function originPattern(baseUrl) {
	const parsed = new URL(baseUrl);
	return `${parsed.protocol}//${parsed.hostname}/*`;
}

async function ensureOriginPermission(origin) {
	if (!ext.permissions || !ext.permissions.contains || !ext.permissions.request) {
		return true;
	}
	const hasPermission = await ext.permissions.contains({ origins: [origin] });
	if (hasPermission) {
		return true;
	}
	return ext.permissions.request({ origins: [origin] });
}

function setDisabled(disabled) {
	for (const element of form.elements) {
		element.disabled = disabled;
	}
}

function showStatus(message, kind) {
	statusNode.textContent = message;
	statusNode.dataset.kind = kind;
}

function messageFor(err) {
	return err instanceof Error ? err.message : String(err);
}
