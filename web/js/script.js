(function () {
  "use strict";

  var statusEl = document.getElementById("status-message");
  var logEl = document.getElementById("log-output");
  var lastLength = 0;
  var redirecting = false;

  function pollStatus() {
    fetch("/status.txt")
      .then(function (resp) {
        if (!resp.ok) {
          // status.txt removed — VM is ready
          if (!redirecting) {
            redirecting = true;
            statusEl.textContent = "VM is ready! Redirecting to console...";
            setTimeout(function () {
              window.location.href = "/vnc.html?autoconnect=1&resize=scale";
            }, 1000);
          }
          return null;
        }
        return resp.text();
      })
      .then(function (text) {
        if (text === null) return;
        if (text.length > lastLength) {
          var newContent = text.substring(lastLength);
          logEl.textContent += newContent;
          lastLength = text.length;
          var container = document.getElementById("log-container");
          container.scrollTop = container.scrollHeight;
          // Show last non-empty line as status
          var lines = text.trim().split("\n");
          var last = lines[lines.length - 1];
          if (last) statusEl.textContent = last;
        }
      })
      .catch(function () {
        // Network error or file not found — keep polling
      });
  }

  setInterval(pollStatus, 1500);
  pollStatus();
})();
