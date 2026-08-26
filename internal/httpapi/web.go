package httpapi

import (
	"errors"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/watchers-factory/raze-posting/internal/application"
	"github.com/watchers-factory/raze-posting/internal/domain"
	"github.com/watchers-factory/raze-posting/internal/rules"
)

type credentialsRequest struct {
	Login    string `json:"login"`
	Password string `json:"password"`
}

type createUserBatchRequest struct {
	Batch             application.CreateBatchRequest `json:"batch"`
	AutoStopSpend     float64                        `json:"auto_stop_spend,omitempty"`
	RuleLookbackHours int64                          `json:"rule_lookback_hours,omitempty"`
}

func (s *Server) registerUser(c fiber.Ctx) error {
	var request credentialsRequest
	if err := decodeJSON(c, &request); err != nil {
		return err
	}
	session, err := s.service.RegisterUser(c.Context(), request.Login, request.Password)
	if err != nil {
		return err
	}
	s.setUserCookies(c, session)
	return jsonOK(c, http.StatusCreated, fiber.Map{"user": session.User})
}

func (s *Server) loginUser(c fiber.Ctx) error {
	var request credentialsRequest
	if err := decodeJSON(c, &request); err != nil {
		return err
	}
	session, err := s.service.LoginUser(c.Context(), request.Login, request.Password)
	if err != nil {
		return err
	}
	s.setUserCookies(c, session)
	return jsonOK(c, http.StatusOK, fiber.Map{"user": session.User})
}

func (s *Server) logoutUser(c fiber.Ctx) error {
	if err := s.service.LogoutUser(c.Context(), c.Cookies(userSessionCookie)); err != nil {
		return err
	}
	s.clearUserCookies(c)
	return c.SendStatus(http.StatusNoContent)
}

func (s *Server) requireUser(c fiber.Ctx) error {
	session, err := s.service.AuthenticateUserSession(c.Context(), c.Cookies(userSessionCookie))
	if err != nil {
		if c.Path() == "/app" || strings.HasPrefix(c.Get("Accept"), "text/html") {
			return c.Redirect().To("/login")
		}
		return err
	}
	c.Locals(userSessionLocal, session)
	return c.Next()
}

func (s *Server) requireCSRF(c fiber.Ctx) error {
	session, err := currentUserSession(c)
	if err != nil {
		return err
	}
	token := strings.TrimSpace(c.Get("X-CSRF-Token"))
	if token == "" || token != c.Cookies(userCSRFCookie) {
		return application.ErrUnauthorized
	}
	if err := s.service.ValidateCSRF(c.Context(), session.SessionID, token); err != nil {
		return err
	}
	return c.Next()
}

func currentUserSession(c fiber.Ctx) (application.AuthSession, error) {
	value := c.Locals(userSessionLocal)
	session, ok := value.(application.AuthSession)
	if !ok || session.User.ID == uuid.Nil {
		return application.AuthSession{}, application.ErrSessionExpired
	}
	return session, nil
}

func (s *Server) setUserCookies(c fiber.Ctx, session application.AuthSession) {
	maxAge := int(time.Until(session.ExpiresAt).Seconds())
	c.Cookie(&fiber.Cookie{
		Name: userSessionCookie, Value: session.Token, Path: "/", MaxAge: maxAge,
		Expires: session.ExpiresAt, HTTPOnly: true, Secure: s.secureCookies, SameSite: "Lax",
	})
	c.Cookie(&fiber.Cookie{
		Name: userCSRFCookie, Value: session.CSRFToken, Path: "/", MaxAge: maxAge,
		Expires: session.ExpiresAt, HTTPOnly: false, Secure: s.secureCookies, SameSite: "Lax",
	})
}

func (s *Server) clearUserCookies(c fiber.Ctx) {
	past := time.Unix(1, 0)
	for _, name := range []string{userSessionCookie, userCSRFCookie} {
		c.Cookie(&fiber.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1, Expires: past, HTTPOnly: name == userSessionCookie, Secure: s.secureCookies, SameSite: "Lax"})
	}
}

func (s *Server) startUserOAuthRedirect(c fiber.Ctx) error {
	session, err := currentUserSession(c)
	if err != nil {
		return err
	}
	result, err := s.service.StartOAuthForUser(c.Context(), session.User.ID)
	if err != nil {
		return err
	}
	return c.Redirect().To(result.AuthorizationURL)
}

func (s *Server) appOverview(c fiber.Ctx) error {
	session, err := currentUserSession(c)
	if err != nil {
		return err
	}
	connections, err := s.service.Repos.Users.ListConnections(c.Context(), session.User.ID)
	if err != nil {
		return err
	}
	accounts, err := s.service.Repos.Users.ListAdAccounts(c.Context(), session.User.ID, 500)
	if err != nil {
		return err
	}
	assets, err := s.service.Repos.Users.ListAssets(c.Context(), session.User.ID, 1000)
	if err != nil {
		return err
	}
	batches, err := s.service.Repos.Users.ListBatches(c.Context(), session.User.ID, 50)
	if err != nil {
		return err
	}
	automationRules, err := s.service.Repos.Users.ListRules(c.Context(), session.User.ID, 50)
	if err != nil {
		return err
	}
	return jsonOK(c, http.StatusOK, fiber.Map{
		"user": session.User, "connections": connections, "ad_accounts": accounts, "assets": assets,
		"batches": batches, "rules": automationRules,
	})
}

func (s *Server) syncUserConnection(c fiber.Ctx) error {
	session, err := currentUserSession(c)
	if err != nil {
		return err
	}
	id, err := parseID(c.Params("id"), "id")
	if err != nil {
		return err
	}
	if err := s.service.Repos.Users.OwnsConnection(c.Context(), session.User.ID, id); err != nil {
		return err
	}
	job, err := s.service.EnqueueConnectionSync(c.Context(), id, "web:"+getRequestID(c))
	if err != nil {
		return err
	}
	return jsonOK(c, http.StatusAccepted, job)
}

func (s *Server) createUserBatch(c fiber.Ctx) error {
	session, err := currentUserSession(c)
	if err != nil {
		return err
	}
	var request createUserBatchRequest
	if err := decodeJSON(c, &request); err != nil {
		return err
	}
	if err := s.service.Repos.Users.OwnsConnection(c.Context(), session.User.ID, request.Batch.ConnectionID); err != nil {
		return err
	}
	for _, accountID := range request.Batch.AdAccountIDs {
		if err := s.service.Repos.Users.OwnsAdAccount(c.Context(), session.User.ID, accountID); err != nil {
			return err
		}
	}
	request.Batch.CreatedBy = "user:" + session.User.ID.String()
	batch, err := s.service.CreateBatch(c.Context(), request.Batch)
	if err != nil {
		return err
	}
	var rule *domain.AutomationRule
	if request.AutoStopSpend > 0 {
		lookbackHours := request.RuleLookbackHours
		if lookbackHours <= 0 {
			lookbackHours = 24
		}
		rule, err = s.service.CreateRule(c.Context(), application.CreateRuleRequest{
			ConnectionID: request.Batch.ConnectionID,
			BatchID:      &batch.ID,
			Name:         "Auto-stop " + batch.Name,
			Status:       domain.RuleActive,
			ScopeLevel:   domain.InsightCampaign,
			Action:       domain.RuleActionPause,
			Conditions: rules.Group{Logic: rules.LogicAll, Conditions: []rules.Condition{{
				Metric: "spend", Operator: rules.OperatorGTE, Threshold: request.AutoStopSpend,
			}}},
			LookbackSeconds:           lookbackHours * 3600,
			EvaluationIntervalSeconds: 300,
		})
		if err != nil {
			return err
		}
	}
	return jsonOK(c, http.StatusAccepted, fiber.Map{"batch": batch, "rule": rule})
}

func (s *Server) createUserRule(c fiber.Ctx) error {
	session, err := currentUserSession(c)
	if err != nil {
		return err
	}
	var request application.CreateRuleRequest
	if err := decodeJSON(c, &request); err != nil {
		return err
	}
	if err := s.service.Repos.Users.OwnsConnection(c.Context(), session.User.ID, request.ConnectionID); err != nil {
		return err
	}
	if request.AdAccountID != nil {
		if err := s.service.Repos.Users.OwnsAdAccount(c.Context(), session.User.ID, *request.AdAccountID); err != nil {
			return err
		}
	}
	if request.BatchID != nil {
		if err := s.service.Repos.Users.OwnsBatch(c.Context(), session.User.ID, *request.BatchID); err != nil {
			return err
		}
	}
	rule, err := s.service.CreateRule(c.Context(), request)
	if err != nil {
		return err
	}
	return jsonOK(c, http.StatusCreated, rule)
}

func (s *Server) enableUserRule(c fiber.Ctx) error {
	return s.setUserRuleStatus(c, domain.RuleActive)
}

func (s *Server) disableUserRule(c fiber.Ctx) error {
	return s.setUserRuleStatus(c, domain.RuleDisabled)
}

func (s *Server) setUserRuleStatus(c fiber.Ctx, status domain.RuleStatus) error {
	session, err := currentUserSession(c)
	if err != nil {
		return err
	}
	id, err := parseID(c.Params("id"), "id")
	if err != nil {
		return err
	}
	if err := s.service.Repos.Users.OwnsRule(c.Context(), session.User.ID, id); err != nil {
		return err
	}
	rule, err := s.service.SetRuleStatus(c.Context(), id, status)
	if err != nil {
		return err
	}
	return jsonOK(c, http.StatusOK, rule)
}

func (s *Server) loginPage(c fiber.Ctx) error    { return sendAuthPage(c, false) }
func (s *Server) registerPage(c fiber.Ctx) error { return sendAuthPage(c, true) }

func sendAuthPage(c fiber.Ctx, register bool) error {
	title, endpoint, alternate, alternateURL := "Sign in", "/auth/login", "Create account", "/register"
	if register {
		title, endpoint, alternate, alternateURL = "Create account", "/auth/register", "Sign in", "/login"
	}
	page := strings.NewReplacer("{{TITLE}}", template.HTMLEscapeString(title), "{{ENDPOINT}}", endpoint, "{{ALT}}", alternate, "{{ALT_URL}}", alternateURL).Replace(authPageHTML)
	c.Set("Content-Type", "text/html; charset=utf-8")
	return c.Status(http.StatusOK).SendString(page)
}

func (s *Server) appPage(c fiber.Ctx) error {
	c.Set("Content-Type", "text/html; charset=utf-8")
	return c.Status(http.StatusOK).SendString(appPageHTML)
}

func userOwnsMediaContext(c fiber.Ctx, connectionID, adAccountID *uuid.UUID, service *application.Service) error {
	session, err := currentUserSession(c)
	if err != nil {
		if errors.Is(err, application.ErrSessionExpired) {
			return nil // Internal bearer API route.
		}
		return err
	}
	if connectionID == nil && adAccountID == nil {
		return invalidField("connection_id", "is required for user uploads")
	}
	if connectionID != nil {
		if err := service.Repos.Users.OwnsConnection(c.Context(), session.User.ID, *connectionID); err != nil {
			return err
		}
	}
	if adAccountID != nil {
		if err := service.Repos.Users.OwnsAdAccount(c.Context(), session.User.ID, *adAccountID); err != nil {
			return err
		}
	}
	return nil
}

const authPageHTML = `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>{{TITLE}} · Raze Posting</title><style>
:root{color-scheme:dark;font-family:Inter,ui-sans-serif,system-ui,sans-serif;background:#080b10;color:#eef2f7}*{box-sizing:border-box}body{margin:0;min-height:100vh;display:grid;place-items:center;background:radial-gradient(circle at 20% 10%,#18324a 0,transparent 38%),#080b10}.card{width:min(92vw,420px);padding:32px;border:1px solid #283242;border-radius:22px;background:#10151ddd;box-shadow:0 30px 80px #0008}.eyebrow{color:#72d5ff;text-transform:uppercase;letter-spacing:.14em;font-size:12px}h1{font-size:34px;margin:10px 0 24px}label{display:grid;gap:8px;margin:16px 0;color:#aeb8c7;font-size:14px}input{width:100%;padding:13px 14px;border:1px solid #313d4f;border-radius:11px;background:#090d13;color:white;font:inherit}button{width:100%;padding:13px;border:0;border-radius:11px;background:#65d2ff;color:#041018;font-weight:750;font:inherit;cursor:pointer}.error{min-height:24px;color:#ff8f8f;margin:12px 0}a{color:#86dcff}.alt{text-align:center;color:#8996a8;margin-top:20px}</style></head><body><main class="card"><div class="eyebrow">Raze Posting</div><h1>{{TITLE}}</h1><form id="auth"><label>Login<input name="login" autocomplete="username" minlength="3" maxlength="64" required></label><label>Password<input name="password" type="password" autocomplete="current-password" minlength="10" maxlength="128" required></label><div class="error" id="error"></div><button>Continue</button></form><div class="alt"><a href="{{ALT_URL}}">{{ALT}}</a></div></main><script>
document.querySelector('#auth').addEventListener('submit',async e=>{e.preventDefault();const b=e.submitter;b.disabled=true;const f=new FormData(e.currentTarget);const r=await fetch('{{ENDPOINT}}',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(Object.fromEntries(f))});if(r.ok){location='/app';return}const x=await r.json().catch(()=>({}));document.querySelector('#error').textContent=x.error?.message||'Request failed';b.disabled=false});
</script></body></html>`

const appPageHTML = `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Workspace · Raze Posting</title><style>
:root{color-scheme:dark;font-family:Inter,ui-sans-serif,system-ui,sans-serif;background:#080b10;color:#edf3fa;--panel:#111720;--line:#273140;--muted:#94a1b2;--accent:#67d5ff}*{box-sizing:border-box}body{margin:0;background:linear-gradient(140deg,#0c121a,#07090d 55%);min-height:100vh}header{height:68px;border-bottom:1px solid var(--line);display:flex;align-items:center;justify-content:space-between;padding:0 28px;position:sticky;top:0;background:#080b10ee;backdrop-filter:blur(16px);z-index:3}.brand{font-weight:800;letter-spacing:-.03em}.brand span{color:var(--accent)}button,.button{border:1px solid #334154;background:#161e29;color:#edf3fa;border-radius:10px;padding:10px 13px;font:inherit;cursor:pointer;text-decoration:none}.primary{background:var(--accent);border-color:var(--accent);color:#051018;font-weight:750}.shell{max-width:1440px;margin:auto;padding:28px;display:grid;gap:22px}.hero{display:flex;justify-content:space-between;align-items:end;gap:24px}.hero h1{font-size:clamp(30px,4vw,50px);letter-spacing:-.055em;margin:0}.hero p{color:var(--muted);max-width:680px}.grid{display:grid;grid-template-columns:repeat(12,1fr);gap:18px}.panel{background:var(--panel);border:1px solid var(--line);border-radius:16px;padding:18px;min-width:0}.connections{grid-column:span 4}.accounts{grid-column:span 8}.composer{grid-column:span 12}.panel h2{margin:0 0 16px;font-size:18px}.list{display:grid;gap:10px}.connection{border:1px solid var(--line);border-radius:12px;padding:12px}.row{display:flex;justify-content:space-between;gap:10px;align-items:center}.muted{color:var(--muted);font-size:13px}.pill{font-size:11px;text-transform:uppercase;letter-spacing:.08em;padding:5px 7px;border-radius:99px;background:#1b2933;color:#80dcff}table{border-collapse:collapse;width:100%;font-size:13px}th,td{text-align:left;padding:10px 8px;border-bottom:1px solid #202a37}th{color:var(--muted);font-weight:600}input,select,textarea{width:100%;border:1px solid #344154;border-radius:10px;padding:11px 12px;background:#090e15;color:#edf3fa;font:inherit}textarea{min-height:100px;resize:vertical}.form-grid{display:grid;grid-template-columns:repeat(4,1fr);gap:12px}.field{display:grid;gap:7px;color:var(--muted);font-size:13px}.wide{grid-column:span 2}.full{grid-column:1/-1}.actions{display:flex;gap:10px;align-items:center}.status{min-height:20px;color:#81dfff}.checkbox{width:auto}code{font-family:ui-monospace,SFMono-Regular,monospace;color:#8ee0ff}@media(max-width:900px){.connections,.accounts{grid-column:1/-1}.form-grid{grid-template-columns:1fr 1fr}}@media(max-width:580px){header,.shell{padding-left:16px;padding-right:16px}.form-grid{grid-template-columns:1fr}.wide{grid-column:auto}.hero{align-items:start;flex-direction:column}}
</style></head><body><header><div class="brand">Raze <span>Posting</span></div><div class="actions"><span id="user" class="muted"></span><button id="logout">Log out</button></div></header><main class="shell"><section class="hero"><div><h1>Your Meta workspace</h1><p>Connections, ad accounts, publishing batches and automatic stop rules are isolated to this account.</p></div><a class="button primary" href="/app/connect/meta">Connect Meta account</a></section><section class="grid"><article class="panel connections"><h2>Meta connections</h2><div id="connections" class="list"><span class="muted">Loading…</span></div></article><article class="panel accounts"><div class="row"><h2>Ad accounts</h2><input id="search" placeholder="Filter accounts" style="max-width:240px"></div><div style="overflow:auto"><table><thead><tr><th></th><th>Account</th><th>Status</th><th>Currency</th><th>Balance (minor)</th><th>Spent (minor)</th></tr></thead><tbody id="accounts"></tbody></table></div></article><article class="panel composer"><h2>Media</h2><form id="media" class="form-grid"><label class="field wide">Connection<select name="connection_id" required></select></label><label class="field">Type<select name="kind"><option value="image">Image</option><option value="video">Video</option></select></label><label class="field">File<input name="file" type="file" accept="image/*,video/*" required></label><div class="field full"><div class="actions"><button>Upload media</button><span id="media-status" class="status"></span></div></div></form><h2 style="margin-top:28px">Launch a campaign</h2><form id="campaign" class="form-grid"><label class="field wide">Connection<select name="connection_id" required></select></label><label class="field wide">Batch name<input name="name" value="Test campaign" required></label><label class="field">Objective<select name="objective"><option>OUTCOME_SALES</option><option>OUTCOME_TRAFFIC</option><option>OUTCOME_LEADS</option><option>OUTCOME_ENGAGEMENT</option><option>OUTCOME_AWARENESS</option><option>OUTCOME_APP_PROMOTION</option></select></label><label class="field">Daily budget (minor units)<input name="budget" type="number" min="1" value="100" required></label><label class="field">Country<input name="country" value="AE" maxlength="2" required></label><label class="field">Auto-stop spend<input name="stop_spend" type="number" min="0" step="0.01" value="0.10"></label><label class="field wide">Page<select name="page_id" required><option value="">Select a synced Page</option></select></label><label class="field wide">Landing URL<input name="link" type="url" required></label><label class="field wide">Primary text<textarea name="message" required>Test campaign</textarea></label><label class="field wide">Headline<input name="headline" value="Learn more" required></label><label class="field wide">Media UUID (optional)<input name="media_id" placeholder="Upload above or paste an existing media ID"></label><label class="field">Leave paused<select name="leave_paused"><option value="true">Yes — safe test</option><option value="false">No — activate</option></select></label><div class="field full"><div class="actions"><button class="primary">Queue batch</button><span id="campaign-status" class="status"></span></div></div></form></article></section></main><script>
const csrf=()=>decodeURIComponent((document.cookie.match(/(?:^|; )raze_csrf=([^;]*)/)||[])[1]||'');let state={connections:[],ad_accounts:[]};const esc=s=>String(s??'');
async function load(){const r=await fetch('/app/api/overview');if(!r.ok){if(r.status===401)location='/login';return}state=await r.json();document.querySelector('#user').textContent=state.user.login;render()}
function render(){const cs=document.querySelector('#connections');cs.replaceChildren(...state.connections.map(c=>{const d=document.createElement('div');d.className='connection';const top=document.createElement('div');top.className='row';const name=document.createElement('strong');name.textContent=c.display_name||c.meta_user_id;const pill=document.createElement('span');pill.className='pill';pill.textContent=c.status;top.append(name,pill);const meta=document.createElement('div');meta.className='muted';meta.textContent=(c.last_synced_at?'Synced '+new Date(c.last_synced_at).toLocaleString():'Not synced');const b=document.createElement('button');b.textContent='Sync';b.style.marginTop='10px';b.onclick=()=>sync(c.id,b);d.append(top,meta,b);return d}));if(!state.connections.length)cs.innerHTML='<span class="muted">Connect a Meta account to begin.</span>';document.querySelectorAll('select[name=connection_id]').forEach(sel=>sel.replaceChildren(...state.connections.map(c=>new Option(c.display_name||c.meta_user_id,c.id))));renderPages();renderHistory();filterAccounts()}
function renderHistory(){let history=document.querySelector('#history');if(!history){history=document.createElement('article');history.id='history';history.className='panel';history.style.gridColumn='span 12';document.querySelector('.composer').before(history)}history.replaceChildren();const heading=document.createElement('h2');heading.textContent='Batches and stop rules';history.append(heading);const batches=document.createElement('div');batches.className='list';(state.batches||[]).forEach(b=>{const d=document.createElement('div');d.className='connection';d.textContent=(b.name||b.id)+' · '+b.status+' · '+b.succeeded_accounts+'/'+b.total_accounts+' succeeded';batches.append(d)});const rulesList=document.createElement('div');rulesList.className='list';rulesList.style.marginTop='12px';(state.rules||[]).forEach(rule=>{const d=document.createElement('div');d.className='connection row';const label=document.createElement('span');label.textContent=rule.name+' · '+rule.status;const button=document.createElement('button');button.textContent=rule.status==='active'?'Disable':'Enable';button.onclick=()=>toggleRule(rule,button);d.append(label,button);rulesList.append(d)});if(!(state.batches||[]).length&&!(state.rules||[]).length){const empty=document.createElement('span');empty.className='muted';empty.textContent='No batches or rules yet.';history.append(empty)}else history.append(batches,rulesList)}
async function toggleRule(rule,button){button.disabled=true;const action=rule.status==='active'?'disable':'enable',r=await fetch('/app/api/rules/'+rule.id+'/'+action,{method:'POST',headers:{'X-CSRF-Token':csrf()}});if(r.ok)await load();else button.disabled=false}
function renderPages(){const connection=document.querySelector('#campaign [name=connection_id]').value,pageSelect=document.querySelector('#campaign [name=page_id]');pageSelect.replaceChildren(new Option('Select a synced Page',''),...(state.assets||[]).filter(a=>a.asset_type==='page'&&a.connection_id===connection).map(a=>new Option(a.name||a.meta_asset_id,a.meta_asset_id)))}
function filterAccounts(){const q=document.querySelector('#search').value.toLowerCase(),connection=document.querySelector('#campaign [name=connection_id]').value;const rows=state.ad_accounts.filter(a=>a.connection_id===connection&&(a.name+' '+a.account_id).toLowerCase().includes(q)).map(a=>{const tr=document.createElement('tr');const values=['',a.name||a.account_id,a.account_status===1&&a.is_active?'Active':'Unavailable',a.currency,a.balance,a.amount_spent_minor];values.forEach((v,i)=>{const td=document.createElement('td');if(i===0){const x=document.createElement('input');x.type='checkbox';x.className='checkbox account-select';x.value=a.id;x.dataset.connection=a.connection_id;td.append(x)}else td.textContent=v;tr.append(td)});return tr});document.querySelector('#accounts').replaceChildren(...rows)}
document.querySelector('#search').oninput=filterAccounts;async function sync(id,b){b.disabled=true;await fetch('/app/api/connections/'+id+'/sync',{method:'POST',headers:{'X-CSRF-Token':csrf()}});b.textContent='Queued'}
document.querySelector('#campaign [name=connection_id]').onchange=()=>{renderPages();filterAccounts()};
document.querySelector('#logout').onclick=async()=>{await fetch('/auth/logout',{method:'POST',headers:{'X-CSRF-Token':csrf()}});location='/login'};
document.querySelector('#media').onsubmit=async e=>{e.preventDefault();const out=document.querySelector('#media-status');out.textContent='Uploading…';const r=await fetch('/app/api/media',{method:'POST',headers:{'X-CSRF-Token':csrf()},body:new FormData(e.currentTarget)});const x=await r.json().catch(()=>({}));if(r.ok){out.textContent='Ready: '+x.id;document.querySelector('#campaign [name=media_id]').value=x.id}else out.textContent=x.error?.message||'Upload failed'};
document.querySelector('#campaign').onsubmit=async e=>{e.preventDefault();const f=new FormData(e.currentTarget),connection=f.get('connection_id');const accountIds=[...document.querySelectorAll('.account-select:checked')].filter(x=>x.dataset.connection===connection).map(x=>x.value);const out=document.querySelector('#campaign-status');if(!accountIds.length){out.textContent='Select at least one account from this connection';return}const hierarchy={campaign:{name:f.get('name'),objective:f.get('objective'),special_ad_categories:[]},ad_set:{name:f.get('name')+' / Ad set',billing_event:'IMPRESSIONS',optimization_goal:f.get('objective')==='OUTCOME_TRAFFIC'?'LINK_CLICKS':'OFFSITE_CONVERSIONS',destination_type:'WEBSITE',daily_budget:Number(f.get('budget')),targeting:{age_min:18,age_max:65,geo_locations:{countries:[f.get('country').toUpperCase()]},publisher_platforms:['facebook','instagram']}},creative:{name:f.get('name')+' / Creative',object_story_spec:{page_id:f.get('page_id'),link_data:{link:f.get('link'),message:f.get('message'),name:f.get('headline'),call_to_action:{type:'LEARN_MORE',value:{link:f.get('link')}}}}},ad:{name:f.get('name')+' / Ad'}};const media=f.get('media_id').trim();const batch={connection_id:connection,name:f.get('name'),ad_account_ids:accountIds,idempotency_key:crypto.randomUUID(),hierarchy,leave_paused:f.get('leave_paused')==='true',media_bindings:media?[{media_id:media,target:'/creative/object_story_spec/link_data/image_hash'}]:[]};out.textContent='Queueing…';const r=await fetch('/app/api/batches',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()},body:JSON.stringify({batch,auto_stop_spend:Number(f.get('stop_spend')),rule_lookback_hours:24})});const x=await r.json().catch(()=>({}));out.textContent=r.ok?'Queued batch '+x.batch.id:(x.error?.message||'Failed')};load();
</script></body></html>`
