/* NexusOS Operator UI — vanilla JS, talks to existing HTTPS JSON API. */
(function () {
  "use strict";

  const STORAGE_KEY = "nexusos.operator.ui.v1";

  const state = {
    view: "dashboard",
    cfg: loadConfig(),
    connected: false,
    lastError: "",
    cache: {},
  };

  function loadConfig() {
    try {
      const raw = localStorage.getItem(STORAGE_KEY);
      if (raw) return JSON.parse(raw);
    } catch (_) {}
    return { baseURL: "", token: "" };
  }

  function saveConfig() {
    localStorage.setItem(
      STORAGE_KEY,
      JSON.stringify({ baseURL: state.cfg.baseURL || "", token: state.cfg.token || "" })
    );
  }

  function apiBase() {
    const b = (state.cfg.baseURL || "").trim().replace(/\/$/, "");
    return b || window.location.origin;
  }

  async function api(method, path, body) {
    const headers = { Accept: "application/json" };
    if (state.cfg.token) headers.Authorization = "Bearer " + state.cfg.token;
    if (body !== undefined) headers["Content-Type"] = "application/json";
    const res = await fetch(apiBase() + path, {
      method,
      headers,
      body: body !== undefined ? JSON.stringify(body) : undefined,
    });
    const text = await res.text();
    let data = null;
    if (text) {
      try {
        data = JSON.parse(text);
      } catch (_) {
        data = { raw: text };
      }
    }
    if (!res.ok) {
      const msg = (data && data.error) || res.statusText || "request failed";
      const err = new Error(msg);
      err.status = res.status;
      err.data = data;
      throw err;
    }
    return data;
  }

  function $(id) {
    return document.getElementById(id);
  }

  function setError(msg) {
    state.lastError = msg || "";
    const el = $("error-banner");
    if (!msg) {
      el.classList.remove("show");
      el.textContent = "";
      return;
    }
    el.textContent = msg;
    el.classList.add("show");
  }

  function setConn(ok, label) {
    state.connected = !!ok;
    const dot = $("conn-dot");
    dot.classList.toggle("ok", !!ok);
    dot.classList.toggle("warn", !ok);
    $("conn-label").textContent = label || (ok ? "connected" : "disconnected");
  }

  function esc(s) {
    return String(s == null ? "" : s)
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;");
  }

  function shortId(id) {
    if (!id) return "—";
    if (id.length <= 16) return id;
    return id.slice(0, 8) + "…" + id.slice(-6);
  }

  function statusBadge(status) {
    const s = String(status || "");
    let cls = "";
    const low = s.toLowerCase();
    if (low === "online" || low === "running" || low === "completed" || low === "ok") cls = "ok";
    else if (low === "offline" || low === "failed" || low === "error") cls = "bad";
    else if (low === "migrating" || low === "draining" || low === "pending" || low === "proposed") cls = "warn";
    return '<span class="badge ' + cls + '">' + esc(s || "—") + "</span>";
  }

  function showView(name) {
    state.view = name;
    document.querySelectorAll(".nav button").forEach((b) => {
      b.classList.toggle("active", b.dataset.view === name);
    });
    document.querySelectorAll(".view").forEach((v) => {
      v.hidden = v.id !== "view-" + name;
    });
  }

  /* ---------- Dashboard ---------- */

  async function refreshDashboard() {
    const [health, node, nodes, members, peers, ledger] = await Promise.all([
      api("GET", "/health"),
      api("GET", "/v1/node"),
      api("GET", "/v1/nodes"),
      api("GET", "/v1/members"),
      api("GET", "/v1/peers").catch(() => ({ peers: [] })),
      api("GET", "/v1/ledger"),
    ]);
    state.cache = { health, node, nodes, members, peers, ledger };

    const nNodes = (nodes.nodes || []).length;
    const online = nodes.online != null ? nodes.online : (nodes.nodes || []).filter((n) => n.status === "Online").length;
    const offline = nodes.offline != null ? nodes.offline : nNodes - online;
    const nMembers = (members.members || []).length;
    const nPeers = (peers.peers || []).length;
    const st = ledger || {};
    const nCtr = Object.keys(st.containers || {}).length;
    const nImg = Object.keys(st.images || {}).length;
    const nWl = Object.keys(st.workloads || {}).length;
    const nMig = Object.keys(st.migrations || {}).length;

    $("dash-stats").innerHTML = [
      stat("Online nodes", online),
      stat("Offline", offline),
      stat("Members", nMembers),
      stat("Peers", nPeers),
      stat("Containers", nCtr),
      stat("Images", nImg),
      stat("Workloads", nWl),
      stat("Migrations", nMig),
    ].join("");

    $("dash-node").innerHTML =
      '<table><tbody>' +
      row("Node ID", '<span class="mono">' + esc(node.node_id) + "</span>") +
      row("Mode", esc(node.mode)) +
      row("Advertise", esc(node.advertise || "—")) +
      row("Phase", esc(node.phase || "—")) +
      row("Consensus", node.consensus ? "yes" : "no") +
      row("Health", statusBadge(health.status)) +
      "</tbody></table>";

    $("dash-nodes").innerHTML = renderNodesTable(nodes.nodes || []);
    $("dash-members").innerHTML = renderMembersTable(members.members || [], members.open);
    $("dash-peers").innerHTML = renderPeersTable(peers.peers || []);
    $("dash-ledger").innerHTML =
      "<p class=\"hint\">Heartbeat timeout: " +
      esc(nodes.heartbeat_timeout || "—") +
      ". Membership open: " +
      (members.open ? "yes (empty set)" : "no") +
      ".</p>" +
      '<div class="grid">' +
      stat("Ledger containers", nCtr) +
      stat("Verified images", nImg) +
      stat("Workloads", nWl) +
      stat("Migrations", nMig) +
      stat("Tombstones", Object.keys(st.tombstones || {}).length) +
      "</div>";
  }

  function stat(label, value) {
    return (
      '<div class="stat"><div class="label">' +
      esc(label) +
      '</div><div class="value">' +
      esc(value) +
      "</div></div>"
    );
  }

  function row(k, v) {
    return "<tr><th style=\"width:9rem\">" + esc(k) + "</th><td>" + v + "</td></tr>";
  }

  function renderNodesTable(list) {
    if (!list.length) return '<p class="empty">No nodes on ledger yet.</p>';
    return (
      "<table><thead><tr><th>Node</th><th>Status</th><th>Addresses</th><th>Last heartbeat</th></tr></thead><tbody>" +
      list
        .map(
          (n) =>
            "<tr><td class=\"mono truncate\" title=\"" +
            esc(n.node_id) +
            "\">" +
            esc(shortId(n.node_id)) +
            "</td><td>" +
            statusBadge(n.status) +
            "</td><td class=\"mono\">" +
            esc((n.addresses || []).join(", ") || "—") +
            "</td><td class=\"mono\">" +
            esc(fmtTime(n.last_heartbeat)) +
            "</td></tr>"
        )
        .join("") +
      "</tbody></table>"
    );
  }

  function renderMembersTable(list, open) {
    if (!list.length) {
      return '<p class="empty">No members' + (open ? " (membership open)" : "") + ".</p>";
    }
    return (
      "<table><thead><tr><th>Node</th><th>Label</th><th>Addresses</th></tr></thead><tbody>" +
      list
        .map(
          (m) =>
            "<tr><td class=\"mono truncate\" title=\"" +
            esc(m.node_id) +
            "\">" +
            esc(shortId(m.node_id)) +
            "</td><td>" +
            esc(m.label || "—") +
            "</td><td class=\"mono\">" +
            esc((m.addresses || []).join(", ") || "—") +
            "</td></tr>"
        )
        .join("") +
      "</tbody></table>"
    );
  }

  function renderPeersTable(list) {
    if (!list.length) return '<p class="empty">No peers configured.</p>';
    return (
      "<table><thead><tr><th>URL</th><th>Node</th><th>Last sync</th></tr></thead><tbody>" +
      list
        .map(
          (p) =>
            "<tr><td class=\"mono\">" +
            esc(p.url || p.URL || "—") +
            "</td><td class=\"mono\">" +
            esc(shortId(p.node_id || p.NodeID || "")) +
            "</td><td class=\"mono\">" +
            esc(fmtTime(p.last_sync || p.LastSync)) +
            "</td></tr>"
        )
        .join("") +
      "</tbody></table>"
    );
  }

  function fmtTime(t) {
    if (!t) return "—";
    try {
      const d = new Date(t);
      if (isNaN(d.getTime())) return String(t);
      return d.toISOString().replace("T", " ").replace(/\.\d+Z$/, "Z");
    } catch (_) {
      return String(t);
    }
  }

  /* ---------- Containers ---------- */

  async function refreshContainers() {
    const [local, ledger] = await Promise.all([
      api("GET", "/v1/containers"),
      api("GET", "/v1/ledger"),
    ]);
    const list = local.containers || [];
    if (!list.length) {
      $("containers-table").innerHTML = '<p class="empty">No local containers.</p>';
    } else {
      $("containers-table").innerHTML =
        "<table><thead><tr><th>ID</th><th>Name</th><th>State</th><th>Image</th><th></th></tr></thead><tbody>" +
        list
          .map((c) => {
            const id = c.id || c.ID;
            return (
              "<tr><td class=\"mono truncate\" title=\"" +
              esc(id) +
              "\">" +
              esc(shortId(id)) +
              "</td><td>" +
              esc(c.name || c.Name || "—") +
              "</td><td>" +
              statusBadge(c.state || c.State) +
              "</td><td class=\"mono truncate\" title=\"" +
              esc(c.image_ref || c.ImageRef || c.image_digest || "") +
              "\">" +
              esc(shortId(c.image_ref || c.ImageRef || c.image_digest || "—")) +
              '</td><td style="white-space:nowrap">' +
              '<button type="button" class="btn secondary sm" data-stop="' +
              esc(id) +
              '">Stop</button> ' +
              '<button type="button" class="btn danger sm" data-rm="' +
              esc(id) +
              '">Remove</button></td></tr>'
            );
          })
          .join("") +
        "</tbody></table>";
      $("containers-table").querySelectorAll("[data-stop]").forEach((btn) => {
        btn.addEventListener("click", () => stopContainer(btn.getAttribute("data-stop")));
      });
      $("containers-table").querySelectorAll("[data-rm]").forEach((btn) => {
        btn.addEventListener("click", () => removeContainer(btn.getAttribute("data-rm")));
      });
    }

    const placed = Object.values((ledger && ledger.containers) || {});
    if (!placed.length) {
      $("placement-table").innerHTML = '<p class="empty">No placement records.</p>';
    } else {
      $("placement-table").innerHTML =
        "<table><thead><tr><th>Container</th><th>Node</th><th>Desired</th><th>Workload</th><th>Replica</th></tr></thead><tbody>" +
        placed
          .map(
            (c) =>
              "<tr><td class=\"mono truncate\" title=\"" +
              esc(c.container_id) +
              "\">" +
              esc(shortId(c.container_id)) +
              "</td><td class=\"mono\">" +
              esc(shortId(c.current_node)) +
              "</td><td>" +
              statusBadge(c.desired_state) +
              "</td><td class=\"mono\">" +
              esc(c.workload_id || "—") +
              "</td><td>" +
              esc(c.replica_index != null ? c.replica_index : "—") +
              "</td></tr>"
          )
          .join("") +
        "</tbody></table>";
    }
  }

  async function stopContainer(id) {
    try {
      await api("POST", "/v1/containers/" + encodeURIComponent(id) + "/stop", {});
      await refreshContainers();
    } catch (e) {
      setError("Stop failed: " + e.message);
    }
  }

  async function removeContainer(id) {
    if (!confirm("Remove container " + id + "?")) return;
    try {
      await api("DELETE", "/v1/containers/" + encodeURIComponent(id));
      await refreshContainers();
    } catch (e) {
      setError("Remove failed: " + e.message);
    }
  }

  function openStartContainerModal() {
    openModal(
      "<h2>Start container</h2>" +
        '<div class="form-row"><label>Name<input id="m-name" /></label>' +
        '<label>Image ref<input id="m-image" placeholder="docker.io/library/nginx:alpine" size="32" /></label></div>' +
        '<div class="modal-actions"><button type="button" class="btn secondary" data-cancel>Cancel</button>' +
        '<button type="button" class="btn" id="m-go">Start</button></div>',
      () => {
        $("m-go").onclick = async () => {
          const name = $("m-name").value.trim();
          const image_ref = $("m-image").value.trim();
          if (!image_ref) return setError("image_ref required");
          try {
            await api("POST", "/v1/containers", { name, image_ref });
            closeModal();
            await refreshContainers();
          } catch (e) {
            setError("Start failed: " + e.message);
          }
        };
      }
    );
  }

  /* ---------- Workloads ---------- */

  async function refreshWorkloads() {
    const [wl, ledger] = await Promise.all([
      api("GET", "/v1/workloads"),
      api("GET", "/v1/ledger"),
    ]);
    const list = wl.workloads || [];
    const containers = (ledger && ledger.containers) || {};

    if (!list.length) {
      $("workloads-table").innerHTML = '<p class="empty">No workloads.</p>';
      return;
    }

    $("workloads-table").innerHTML = list
      .map((w) => {
        const placements = Object.values(containers).filter((c) => c.workload_id === w.workload_id);
        const placementRows = placements.length
          ? placements
              .map(
                (c) =>
                  "<tr><td>" +
                  esc(c.replica_index != null ? c.replica_index : "—") +
                  '</td><td class="mono truncate" title="' +
                  esc(c.container_id) +
                  '">' +
                  esc(shortId(c.container_id)) +
                  '</td><td class="mono">' +
                  esc(shortId(c.current_node)) +
                  "</td><td>" +
                  statusBadge(c.desired_state) +
                  '</td><td class="mono truncate">' +
                  esc(shortId(c.image_digest)) +
                  "</td></tr>"
              )
              .join("")
          : '<tr><td colspan="5" class="empty">No replica placements yet</td></tr>';
        const st = w.status || {};
        return (
          '<div class="panel" style="background:var(--surface2)">' +
          "<h3 style=\"margin-top:0;color:var(--text)\">" +
          esc(w.workload_id) +
          " " +
          statusBadge(w.strategy || "—") +
          "</h3>" +
          "<p class=\"hint\">replicas " +
          esc(st.desired != null ? st.desired : w.replicas) +
          " desired · " +
          esc(st.current != null ? st.current : "—") +
          " current · " +
          esc(st.available != null ? st.available : "—") +
          " available · max_unavailable " +
          esc(w.max_unavailable != null ? w.max_unavailable : "—") +
          "</p>" +
          '<p class="mono truncate" title="' +
          esc(w.image_digest || w.image_ref || "") +
          '">' +
          esc(w.image_ref || shortId(w.image_digest) || "—") +
          "</p>" +
          '<div class="toolbar">' +
          '<button type="button" class="btn secondary sm" data-scale="' +
          esc(w.workload_id) +
          '">Scale</button> ' +
          '<button type="button" class="btn secondary sm" data-update="' +
          esc(w.workload_id) +
          '">Update strategy</button> ' +
          '<button type="button" class="btn danger sm" data-del-wl="' +
          esc(w.workload_id) +
          '">Delete</button></div>' +
          "<table><thead><tr><th>Replica</th><th>Container</th><th>Node</th><th>Desired</th><th>Image</th></tr></thead><tbody>" +
          placementRows +
          "</tbody></table></div>"
        );
      })
      .join("");

    $("workloads-table").querySelectorAll("[data-scale]").forEach((btn) => {
      btn.addEventListener("click", () => openScaleModal(btn.getAttribute("data-scale")));
    });
    $("workloads-table").querySelectorAll("[data-update]").forEach((btn) => {
      btn.addEventListener("click", () => openUpdateWorkloadModal(btn.getAttribute("data-update"), list));
    });
    $("workloads-table").querySelectorAll("[data-del-wl]").forEach((btn) => {
      btn.addEventListener("click", async () => {
        const id = btn.getAttribute("data-del-wl");
        if (!confirm("Delete workload " + id + "?")) return;
        try {
          await api("DELETE", "/v1/workloads/" + encodeURIComponent(id));
          await refreshWorkloads();
        } catch (e) {
          setError("Delete failed: " + e.message);
        }
      });
    });
  }

  function openCreateWorkloadModal() {
    openModal(
      "<h2>Create workload</h2>" +
        '<div class="form-row">' +
        '<label>Workload ID<input id="m-wl-id" placeholder="web" /></label>' +
        '<label>Replicas<input id="m-wl-rep" type="number" min="0" value="1" /></label>' +
        '<label>Strategy<select id="m-wl-strat"><option value="RollingUpdate">RollingUpdate</option><option value="Recreate">Recreate</option></select></label>' +
        '<label>Max unavailable<input id="m-wl-mu" type="number" min="0" value="1" /></label>' +
        "</div>" +
        '<div class="form-row">' +
        '<label>Image ref<input id="m-wl-ref" placeholder="docker.io/library/nginx:alpine" size="36" /></label>' +
        '<label>Image digest (optional)<input id="m-wl-dig" placeholder="sha256:…" size="28" /></label>' +
        "</div>" +
        '<div class="modal-actions"><button type="button" class="btn secondary" data-cancel>Cancel</button>' +
        '<button type="button" class="btn" id="m-go">Create</button></div>',
      () => {
        $("m-go").onclick = async () => {
          const body = {
            workload_id: $("m-wl-id").value.trim(),
            replicas: Number($("m-wl-rep").value) || 0,
            strategy: $("m-wl-strat").value,
            max_unavailable: Number($("m-wl-mu").value) || 0,
          };
          const ref = $("m-wl-ref").value.trim();
          const dig = $("m-wl-dig").value.trim();
          if (ref) body.image_ref = ref;
          if (dig) body.image_digest = dig;
          if (!body.workload_id) return setError("workload_id required");
          if (!body.image_ref && !body.image_digest) return setError("image_ref or image_digest required");
          try {
            await api("POST", "/v1/workloads", body);
            closeModal();
            await refreshWorkloads();
          } catch (e) {
            setError("Create failed: " + e.message);
          }
        };
      }
    );
  }

  function openScaleModal(id) {
    openModal(
      "<h2>Scale " +
        esc(id) +
        "</h2>" +
        '<div class="form-row"><label>Replicas<input id="m-rep" type="number" min="0" value="1" /></label></div>' +
        '<div class="modal-actions"><button type="button" class="btn secondary" data-cancel>Cancel</button>' +
        '<button type="button" class="btn" id="m-go">Scale</button></div>',
      () => {
        $("m-go").onclick = async () => {
          try {
            await api("POST", "/v1/workloads/" + encodeURIComponent(id) + "/scale", {
              replicas: Number($("m-rep").value) || 0,
            });
            closeModal();
            await refreshWorkloads();
          } catch (e) {
            setError("Scale failed: " + e.message);
          }
        };
      }
    );
  }

  function openUpdateWorkloadModal(id, list) {
    const w = (list || []).find((x) => x.workload_id === id) || {};
    openModal(
      "<h2>Update " +
        esc(id) +
        "</h2>" +
        '<div class="form-row">' +
        '<label>Strategy<select id="m-wl-strat"><option value="RollingUpdate">RollingUpdate</option><option value="Recreate">Recreate</option></select></label>' +
        '<label>Max unavailable<input id="m-wl-mu" type="number" min="0" value="' +
        esc(w.max_unavailable != null ? w.max_unavailable : 1) +
        '" /></label>' +
        '<label>Image ref<input id="m-wl-ref" value="' +
        esc(w.image_ref || "") +
        '" size="28" /></label>' +
        "</div>" +
        '<div class="modal-actions"><button type="button" class="btn secondary" data-cancel>Cancel</button>' +
        '<button type="button" class="btn" id="m-go">Update</button></div>',
      () => {
        const sel = $("m-wl-strat");
        if (w.strategy) sel.value = w.strategy;
        $("m-go").onclick = async () => {
          const body = {
            strategy: $("m-wl-strat").value,
            max_unavailable: Number($("m-wl-mu").value) || 0,
          };
          const ref = $("m-wl-ref").value.trim();
          if (ref) body.image_ref = ref;
          try {
            await api("PUT", "/v1/workloads/" + encodeURIComponent(id), body);
            closeModal();
            await refreshWorkloads();
          } catch (e) {
            setError("Update failed: " + e.message);
          }
        };
      }
    );
  }

  /* ---------- Migrations ---------- */

  async function refreshMigrations() {
    const data = await api("GET", "/v1/migrations");
    const list = data.migrations || [];
    if (!list.length) {
      $("migrations-table").innerHTML = '<p class="empty">No migrations.</p>';
      return;
    }
    $("migrations-table").innerHTML =
      "<table><thead><tr><th>ID</th><th>Container</th><th>From</th><th>To</th><th>Status</th><th>Started</th><th>Finished</th></tr></thead><tbody>" +
      list
        .map(
          (m) =>
            "<tr><td class=\"mono truncate\" title=\"" +
            esc(m.migration_id) +
            "\">" +
            esc(shortId(m.migration_id)) +
            '</td><td class="mono truncate" title="' +
            esc(m.container_id) +
            '">' +
            esc(shortId(m.container_id)) +
            '</td><td class="mono">' +
            esc(shortId(m.from_node)) +
            '</td><td class="mono">' +
            esc(shortId(m.to_node)) +
            "</td><td>" +
            statusBadge(m.status) +
            '</td><td class="mono">' +
            esc(fmtTime(m.started_at)) +
            '</td><td class="mono">' +
            esc(fmtTime(m.finished_at)) +
            "</td></tr>"
        )
        .join("") +
      "</tbody></table>";
  }

  async function openMigrateModal() {
    let nodes = [];
    let containers = [];
    try {
      const [n, l] = await Promise.all([api("GET", "/v1/nodes"), api("GET", "/v1/ledger")]);
      nodes = n.nodes || [];
      containers = Object.values((l && l.containers) || {});
    } catch (e) {
      setError(e.message);
      return;
    }
    const nodeOpts = nodes
      .map((n) => '<option value="' + esc(n.node_id) + '">' + esc(shortId(n.node_id) + " (" + n.status + ")") + "</option>")
      .join("");
    const ctrOpts = containers
      .map(
        (c) =>
          '<option value="' +
          esc(c.container_id) +
          '">' +
          esc(shortId(c.container_id) + " @ " + shortId(c.current_node)) +
          "</option>"
      )
      .join("");
    openModal(
      "<h2>Propose cold migrate</h2>" +
        '<p class="hint">Uses <code>POST /v1/migrations</code> (Phase 4 cold path).</p>' +
        '<div class="form-row">' +
        '<label>Container<select id="m-ctr"><option value="">— select —</option>' +
        ctrOpts +
        '</select></label>' +
        '<label>Or container ID<input id="m-ctr-id" placeholder="container id" size="24" /></label>' +
        "</div>" +
        '<div class="form-row">' +
        '<label>To node<select id="m-to"><option value="">— select —</option>' +
        nodeOpts +
        "</select></label>" +
        '<label>From node (optional)<select id="m-from"><option value="">auto</option>' +
        nodeOpts +
        "</select></label>" +
        "</div>" +
        '<div class="modal-actions"><button type="button" class="btn secondary" data-cancel>Cancel</button>' +
        '<button type="button" class="btn" id="m-go">Migrate</button></div>',
      () => {
        $("m-go").onclick = async () => {
          const container_id = $("m-ctr-id").value.trim() || $("m-ctr").value;
          const to_node = $("m-to").value;
          const from_node = $("m-from").value;
          if (!container_id || !to_node) return setError("container_id and to_node required");
          const body = { container_id, to_node };
          if (from_node) body.from_node = from_node;
          try {
            await api("POST", "/v1/migrations", body);
            closeModal();
            await refreshMigrations();
          } catch (e) {
            setError("Migrate failed: " + e.message);
          }
        };
      }
    );
  }

  /* ---------- Modal / settings / refresh ---------- */

  function openModal(html, after) {
    const backdrop = $("modal");
    $("modal-body").innerHTML = html;
    backdrop.classList.add("show");
    backdrop.querySelectorAll("[data-cancel]").forEach((b) => {
      b.onclick = closeModal;
    });
    if (after) after();
  }

  function closeModal() {
    $("modal").classList.remove("show");
    $("modal-body").innerHTML = "";
  }

  function fillSettingsForm() {
    $("cfg-base").value = state.cfg.baseURL || "";
    $("cfg-token").value = state.cfg.token || "";
    $("settings-status").textContent =
      "Effective API base: " + apiBase() + (state.cfg.token ? " (token set)" : " (no token)");
  }

  async function refreshAll() {
    setError("");
    try {
      if (state.view === "dashboard") await refreshDashboard();
      else if (state.view === "containers") await refreshContainers();
      else if (state.view === "workloads") await refreshWorkloads();
      else if (state.view === "migrations") await refreshMigrations();
      else if (state.view === "settings") fillSettingsForm();
      setConn(true, "connected · " + apiBase().replace(/^https?:\/\//, ""));
    } catch (e) {
      setConn(false, e.status === 401 ? "unauthorized" : "error");
      setError(e.message || String(e));
      if (state.view === "settings") fillSettingsForm();
    }
  }

  function bind() {
    document.querySelectorAll(".nav button").forEach((b) => {
      b.addEventListener("click", () => {
        showView(b.dataset.view);
        refreshAll();
      });
    });
    $("btn-refresh").addEventListener("click", refreshAll);
    $("btn-save-settings").addEventListener("click", () => {
      state.cfg.baseURL = $("cfg-base").value.trim();
      state.cfg.token = $("cfg-token").value;
      saveConfig();
      fillSettingsForm();
      showView("dashboard");
      refreshAll();
    });
    $("btn-clear-token").addEventListener("click", () => {
      state.cfg.token = "";
      $("cfg-token").value = "";
      saveConfig();
      fillSettingsForm();
    });
    $("btn-start-container").addEventListener("click", openStartContainerModal);
    $("btn-create-workload").addEventListener("click", openCreateWorkloadModal);
    $("btn-propose-migrate").addEventListener("click", openMigrateModal);
    $("modal").addEventListener("click", (ev) => {
      if (ev.target === $("modal")) closeModal();
    });
  }

  bind();
  fillSettingsForm();
  showView("dashboard");
  refreshAll();
  setInterval(() => {
    if (state.view !== "settings" && state.connected) refreshAll();
  }, 15000);
})();
