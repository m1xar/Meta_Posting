package httpapi

import (
	"html/template"
	"net/http"

	"github.com/gofiber/fiber/v3"
)

const legalContactEmail = "Mike_AI@raze.media"

func (s *Server) landingPage(c fiber.Ctx) error {
	return sendPublicPage(c, "Raze Posting", `
<main>
  <style>
    .workflow-list{width:min(100% - 3rem,72rem);margin:clamp(2rem,5vw,4rem) auto 0;display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:1px;border:1px solid rgba(244,244,241,.12);border-radius:1.35rem;overflow:hidden;background:rgba(244,244,241,.12)}
    .text-block{min-height:24rem;padding:clamp(1.4rem,3vw,2.4rem);background:#090908;display:flex;flex-direction:column;align-items:flex-start}
    .text-block h3{margin:1rem 0 0;font-size:clamp(1.45rem,2.4vw,2.25rem);font-weight:450;letter-spacing:-.035em;line-height:1.06}
    .text-block>p:not(.step){margin:.9rem 0 0;color:var(--muted);font-size:1rem;line-height:1.6}
    .text-block ul{display:flex;gap:.45rem;flex-wrap:wrap;padding:0;margin:auto 0 0;list-style:none}
    .text-block li{border:1px solid rgba(244,244,241,.14);border-radius:999px;padding:.4rem .58rem;color:var(--muted);font-family:var(--mono);font-size:.64rem;letter-spacing:.08em;text-transform:uppercase}
    .simple-systems{padding:clamp(3rem,7vw,6rem) 0}.simple-systems header{display:grid;justify-items:center;text-align:center;margin-bottom:clamp(1.8rem,4vw,3rem)}
    .system-list{border-top:1px solid rgba(244,244,241,.12)}.system-list article{display:grid;grid-template-columns:minmax(11rem,.42fr) 1fr;gap:1.5rem;align-items:baseline;padding:1.25rem 0;border-bottom:1px solid rgba(244,244,241,.12)}
    .system-list span{color:var(--soft);font-family:var(--mono);font-size:.7rem;letter-spacing:.12em;text-transform:uppercase}.system-list p{max-width:42rem;margin:0;color:var(--muted);font-size:clamp(1rem,1.7vw,1.22rem);line-height:1.45}
    .join{padding-bottom:clamp(4rem,9vw,8rem)}[data-reveal]{opacity:1!important;transform:none!important}
    @media(max-width:820px){.workflow-list{grid-template-columns:1fr}.text-block{min-height:0}.system-list article{grid-template-columns:1fr;gap:.5rem;padding:1.1rem 0}}
  </style>
  <section class="hero" id="top">
    <span class="wash" aria-hidden="true"></span>
    <p class="eyebrow" data-reveal>Meta operations factory</p>
    <h1 data-reveal><span>Raze Posting</span><span class="phrase" data-phrase>keeps launches observable</span></h1>
    <p class="hero-copy" data-reveal>Campaign work is not a calendar. It is a chain of permissions, assets, jobs, failures, approvals, and evidence. Raze Posting gives authorized teams a control layer for Meta workflows before the work turns into tab archaeology.</p>
    <div class="actions" data-reveal>
      <a class="button primary" href="/docs"><span>Open API docs</span></a>
      <a class="button text" href="mailto:Mike_AI@raze.media"><span>Talk to the team</span></a>
    </div>
    <a class="source-pill" href="/privacy" data-reveal><span>Public operating update</span><strong>Privacy, terms &amp; deletion published · 04 Aug 2026</strong></a>
  </section>

  <section class="offers" id="workflows">
    <div class="frame intro" data-reveal>
      <p>How it works</p>
      <h2>One clear place for the work around a campaign.</h2>
      <span>Raze Posting keeps the operational context together: what is connected, what is ready, what needs review, and what happened next.</span>
    </div>
    <div class="frame workflow-list">
      <article class="text-block" data-reveal>
        <p class="step"><span>01</span> Access</p>
        <h3>Start with the assets the team is actually allowed to manage.</h3>
        <p>Connect an authorized Meta account, discover its businesses and ad accounts, then see the scope before an operator prepares work.</p>
        <ul><li>Business IDs</li><li>Ad-account scope</li><li>Connection records</li></ul>
      </article>
      <article class="text-block" data-reveal>
        <p class="step"><span>02</span> Review</p>
        <h3>Prepare publishing and campaign work as a deliberate batch.</h3>
        <p>Keep uploads, creative references and campaign changes together. Review the inputs before any request is sent.</p>
        <ul><li>Media mapping</li><li>Approval gate</li><li>Paused defaults</li></ul>
      </article>
      <article class="text-block" data-reveal>
        <p class="step"><span>03</span> Run</p>
        <h3>Leave a usable record when work completes, pauses or fails.</h3>
        <p>Jobs expose their state, retry window and outcome. The next person has the context to continue without rebuilding the story from browser tabs.</p>
        <ul><li>Job status</li><li>Safe retries</li><li>Review evidence</li></ul>
      </article>
    </div>
  </section>

  <section class="frame systems simple-systems" id="signal" data-reveal>
    <header>
      <p>Product surface</p>
      <h2>Built for the parts of Meta operations that get messy fast.</h2>
    </header>
    <div class="system-list">
      <article><span>Business access</span><p>Keep authorized users, businesses, accounts and Pages in a visible operational boundary.</p></article>
      <article><span>Campaign evidence</span><p>Review campaign structure and performance context before deciding what changes.</p></article>
      <article><span>Campaign operations</span><p>Prepare approved ad changes with their assets and record the result.</p></article>
      <article><span>Media pipeline</span><p>Keep files attached to the account and job they belong to.</p></article>
      <article><span>Job recovery</span><p>Handle rate limits, retries and failures as clear follow-up work.</p></article>
    </div>
  </section>

  <section class="frame intelligence" id="notes" data-reveal>
    <header>
      <p>Field notes</p>
      <h2>Notes for teams who live after the demo.</h2>
      <a class="button text" href="/privacy"><span>Open public info</span></a>
    </header>
    <div class="note-grid">
      <a href="/terms"><span>Operating terms</span><h3>The permission is not the product.</h3><p>Why Raze Posting treats Meta permissions as a scoped operating boundary, not a blank check.</p></a>
      <a href="/privacy"><span>Data handling</span><h3>Connected data needs a small surface.</h3><p>Identifiers, campaign context, media state, and job logs are processed only to run the requested workflow.</p></a>
      <a href="/data-deletion"><span>Deletion</span><h3>The exit should be as legible as the start.</h3><p>Disconnecting a workspace and removing associated records is part of the public operating contract.</p></a>
    </div>
  </section>

  <section class="frame join" data-reveal>
    <span class="glow" aria-hidden="true"></span>
    <div>
      <h2>Bring the workflow before it becomes expensive.</h2>
      <p>Use Raze Posting when the campaign operation has enough moving parts to deserve a control layer.</p>
      <a class="button primary" href="mailto:Mike_AI@raze.media"><span>Mike_AI@raze.media</span></a>
    </div>
  </section>
</main>`)
}

func (s *Server) privacyPolicy(c fiber.Ctx) error {
	return sendLegalPage(c, "Privacy Policy", "Data handling", `
<p class="lede">Raze Posting is a business tool for authorized users who manage advertising and publishing workflows through Meta APIs.</p>
<h2 id="information">Information we process</h2>
<p>When an authorized user connects a Meta account, Raze Posting processes account and business identifiers, ad-account and Page identifiers, access credentials supplied by Meta, campaign and insight data, uploaded media, and operational job logs needed to provide the requested service.</p>
<h2 id="use">How we use information</h2>
<p>We use this information to authenticate the connection, synchronize authorized Meta assets, audit campaigns, prepare publishing batches, run scheduled jobs, and troubleshoot service failures. We do not sell personal information or use connected Meta data for advertising unrelated to the user's account.</p>
<h2 id="security">Storage and security</h2>
<p>Access credentials are stored encrypted at rest. Requests to the service are protected by authentication and HTTPS. We retain operational records only for as long as they are needed to provide the service, meet security obligations, or resolve disputes.</p>
<h2 id="sharing">Sharing</h2>
<p>Raze Posting sends requests to Meta only when required to provide a feature requested by the authorized user. We do not disclose connected account data to unrelated third parties.</p>
<h2 id="choices">Your choices</h2>
<p>You can disconnect a Meta account and request deletion of associated data at any time by contacting <a href="mailto:Mike_AI@raze.media">Mike_AI@raze.media</a>. See the <a href="/data-deletion">data deletion instructions</a>.</p>
<details open><summary>Operational summary</summary><p>The service is designed around scoped access, encrypted credentials, visible jobs, and a clear deletion path.</p></details>
<p class="updated">Last updated: 2026-08-04.</p>`)
}

func (s *Server) termsOfService(c fiber.Ctx) error {
	return sendLegalPage(c, "Terms of Service", "Operating terms", `
<p class="lede">Raze Posting is provided for authorized business users to manage their own advertising and publishing workflows through Meta APIs.</p>
<h2 id="authorized-use">Authorized use</h2>
<p>You may connect only Meta accounts, businesses, Pages, ad accounts, and media that you are authorized to manage. You are responsible for complying with Meta's terms, policies, and applicable law.</p>
<h2 id="service-limits">Service limits</h2>
<p>Features depend on the permissions granted by Meta and the availability of Meta APIs. Raze Posting may pause or retry requests when Meta applies temporary rate limits or permission restrictions.</p>
<h2 id="review">Human review</h2>
<p>The service is intended to support marketing operators. Teams remain responsible for reviewing campaign changes, publishing actions, and media before they are moved forward.</p>
<h2 id="contact">Contact</h2>
<p>Questions about these terms can be sent to <a href="mailto:Mike_AI@raze.media">Mike_AI@raze.media</a>.</p>
<details open><summary>Usage boundary</summary><p>Use the service only for assets you control, workflows you are authorized to run, and operations that follow applicable platform rules.</p></details>
<p class="updated">Last updated: 2026-08-04.</p>`)
}

func (s *Server) dataDeletion(c fiber.Ctx) error {
	return sendLegalPage(c, "Data Deletion Instructions", "Deletion path", `
<p class="lede">To request deletion of data associated with a Raze Posting connection, email <a href="mailto:Mike_AI@raze.media?subject=Raze%20Posting%20data%20deletion">Mike_AI@raze.media</a> from the account email address.</p>
<h2 id="request">What to include</h2>
<p>Include the connected Meta account or business name and, if available, the connection identifier. This helps us verify the request and find the correct service records.</p>
<h2 id="process">What happens next</h2>
<p>We will verify the request, disconnect the Meta connection, and delete the associated stored credentials and service records, except information that must be retained for legal or security purposes.</p>
<h2 id="confirmation">Confirmation</h2>
<p>We will confirm completion by email after the request is processed.</p>
<details open><summary>Fastest route</summary><p>Email from the address associated with the connected account and include the business or Page name in the subject.</p></details>`)
}

func sendLegalPage(c fiber.Ctx, title, kicker, body string) error {
	return sendPublicPage(c, title, `<main class="article-page"><div class="read-progress" aria-hidden="true"></div><article class="article-shell" data-reveal><p class="article-kicker">Raze Posting / `+template.HTMLEscapeString(kicker)+`</p><h1>`+template.HTMLEscapeString(title)+`</h1><div class="article-meta"><span>Public information</span><span>Authorized Meta workflows</span><span>Contact: <a href="mailto:`+legalContactEmail+`">`+legalContactEmail+`</a></span></div><div class="article-body">`+body+`</div></article></main>`)
}

func sendPublicPage(c fiber.Ctx, title, body string) error {
	page := `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><meta name="description" content="Raze Posting - a focused operations workspace for authorized Meta advertising workflows"><title>` + template.HTMLEscapeString(title) + ` - Raze Posting</title><style>
:root{color-scheme:dark;--bg:#050505;--panel:#090908;--panel2:#111110;--ink:#f4f4f1;--muted:#aaa7a0;--soft:#dedbd2;--line:rgba(244,244,241,.14);--strong:rgba(244,244,241,.92);--wash:rgba(244,244,241,.08);--ease:cubic-bezier(.16,1,.3,1);--font:Inter,ui-sans-serif,system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;--mono:"SFMono-Regular",Consolas,"Liberation Mono",monospace}
*{box-sizing:border-box}html{scroll-behavior:smooth}body{margin:0;background:var(--bg);color:var(--ink);font-family:var(--font);line-height:1.55;min-height:100vh;overflow-x:hidden}body:before{content:"";position:fixed;inset:0;pointer-events:none;background:linear-gradient(rgba(244,244,241,.024) 1px,transparent 1px),linear-gradient(90deg,rgba(244,244,241,.018) 1px,transparent 1px);background-size:3px 3px;mix-blend-mode:screen;opacity:.46;-webkit-mask-image:linear-gradient(#000,transparent 88%);mask-image:linear-gradient(#000,transparent 88%)}a{color:inherit;text-decoration:none}.frame{width:min(100% - 3rem,72rem);margin-inline:auto}.site-header{position:fixed;top:1.55rem;left:0;right:0;z-index:30;display:flex;justify-content:center;pointer-events:none;transition:transform .52s var(--ease),opacity .52s var(--ease)}.site-header.hidden{transform:translateY(-125%);opacity:0}.nav-shell{pointer-events:auto;width:min(100% - 3rem,58.5rem);min-height:4.45rem;padding:.45rem .5rem .45rem 1.25rem;border:1px solid var(--line);border-radius:1.65rem;background:rgba(8,8,7,.88);backdrop-filter:blur(24px);display:flex;align-items:center;justify-content:space-between;gap:2rem}.brand{position:relative;display:inline-flex;align-items:center;width:4.4rem;min-height:2.4rem;color:var(--ink)}.brand-mark{width:1.35rem;height:1.35rem;border:1px solid var(--ink);border-radius:.28rem;display:grid;place-items:center;font-weight:800;font-size:.8rem;transition:opacity .9s var(--ease),transform 1.1s var(--ease),filter 1.1s var(--ease)}.brand-name{position:absolute;left:0;top:50%;transform:translateY(-50%) translateX(-.2rem);font-family:var(--mono);font-size:.58rem;letter-spacing:.13em;line-height:1.08;text-transform:uppercase;opacity:0;clip-path:inset(0 100% 0 0);transition:clip-path 1.1s var(--ease),opacity .76s ease,transform 1.1s var(--ease);white-space:pre}.brand:hover .brand-mark{opacity:0;filter:blur(4px);transform:translateX(2.8rem) scale(.88)}.brand:hover .brand-name{opacity:1;clip-path:inset(0 0 0 0);transform:translateY(-50%)}.nav-links{position:absolute;left:50%;transform:translateX(-50%);display:flex;align-items:center;gap:0;height:46px}.nav-links a{height:38px;display:inline-flex;align-items:center;justify-content:center;padding:0 1rem;border-radius:.78rem;color:rgba(244,244,241,.7);font-size:.98rem;font-weight:500;transition:color .16s ease,background .16s ease}.nav-links a:hover{color:#fff;background:rgba(244,244,241,.07)}.nav-cta,.button{display:inline-flex;align-items:center;justify-content:center;border:1px solid transparent;border-radius:.82rem;min-height:2.8rem;padding:0 1.15rem;font-family:var(--mono);font-size:.76rem;font-weight:700;letter-spacing:.075em;text-transform:uppercase;transition:background .22s var(--ease),border-color .22s var(--ease),color .22s var(--ease),transform .2s var(--ease)}.nav-cta,.button.primary{background:var(--ink);border-color:var(--ink);color:#080807}.nav-cta:hover,.button.primary:hover{background:#fff;color:#050505;transform:translateY(-1px)}.button.text{background:rgba(244,244,241,.035);border-color:rgba(244,244,241,.16);color:var(--ink)}.button.text:hover{background:rgba(244,244,241,.1);border-color:rgba(244,244,241,.34)}.menu-button{display:none}.hero{min-height:calc(100svh - 4.5rem);display:grid;place-items:center;text-align:center;position:relative;overflow:hidden;padding:clamp(7rem,12vw,10rem) 1.5rem clamp(4rem,7vw,6rem);box-shadow:inset 0 -7rem 5rem -5rem rgba(5,5,4,.88)}.hero:before{content:"";position:absolute;inset:0;z-index:-2;background:radial-gradient(circle at 18% 16%,rgba(244,244,241,.13),transparent 30rem),radial-gradient(circle at 72% 34%,rgba(255,255,255,.08),transparent 34rem),linear-gradient(180deg,#070707,#0b0b0a 52%,#050504)}.hero:after{content:"";position:absolute;inset:0;z-index:-1;pointer-events:none;background:linear-gradient(rgba(244,244,241,.027) 1px,transparent 0),linear-gradient(90deg,rgba(244,244,241,.022) 1px,transparent 0);background-size:3px 3px;opacity:.44;-webkit-mask-image:linear-gradient(180deg,rgba(0,0,0,.72),transparent 92%);mask-image:linear-gradient(180deg,rgba(0,0,0,.72),transparent 92%)}.wash{position:absolute;inset:10% 8% auto;height:38rem;border-radius:999px;background:radial-gradient(ellipse at center,rgba(244,244,241,.1),transparent 68%);filter:blur(12px);z-index:-1}.eyebrow,.article-kicker{margin:0 0 1.15rem;color:var(--soft);font-family:var(--mono);font-size:.68rem;letter-spacing:.42em;line-height:1;text-transform:uppercase}.hero h1{display:grid;justify-items:center;gap:.05em;margin:0;color:var(--ink);font-size:clamp(2.9rem,6.5vw,6.1rem);font-weight:430;letter-spacing:-.045em;line-height:1;width:min(100%,68rem)}.hero h1 span{display:block}.phrase{min-height:3.05em;max-width:13.5ch;transition:opacity .52s var(--ease),filter .52s var(--ease),transform .52s var(--ease)}.phrase.swap{opacity:0;filter:blur(6px);transform:translateY(-.15em)}.hero-copy{color:var(--muted);font-size:clamp(1rem,1.6vw,1.25rem);letter-spacing:-.018em;line-height:1.55;margin:clamp(1.4rem,3vw,2rem) 0 0;width:min(100%,45rem)}.actions{display:flex;flex-wrap:wrap;gap:.7rem;justify-content:center;margin-top:clamp(1.6rem,3vw,2.4rem)}.source-pill{display:inline-flex;align-items:center;gap:.65rem;margin-top:clamp(2.6rem,5vw,4.8rem);padding:.55rem .85rem;border:1px solid rgba(244,244,241,.12);border-radius:999px;background:rgba(244,244,241,.055);color:var(--muted);font-size:.76rem;transition:border-color .22s var(--ease),background .22s var(--ease)}.source-pill:hover{background:rgba(244,244,241,.08);border-color:rgba(244,244,241,.3)}.source-pill span{font-family:var(--mono);letter-spacing:.12em;text-transform:uppercase}.source-pill strong{color:var(--ink);font-weight:500}.offers{padding:clamp(4.5rem,9vw,8rem) 0;position:relative;background:linear-gradient(180deg,rgba(5,5,4,.7),#0a0a09 58%,#050504)}.offers:before{content:"";position:absolute;inset:0 0 auto;height:7rem;transform:translateY(-99%);background:linear-gradient(180deg,rgba(5,5,4,0),rgba(5,5,4,.8));pointer-events:none}.intro{display:grid;justify-items:center;text-align:center;margin-bottom:clamp(2rem,5vw,4rem)}.intro p,.step,.systems header p,.intelligence header p{color:var(--soft);font-family:var(--mono);font-size:.72rem;letter-spacing:.24em;line-height:1;margin:0;text-transform:uppercase}.intro h2,.systems header h2,.intelligence header h2{font-size:clamp(2.8rem,6.6vw,6.2rem);font-weight:430;letter-spacing:-.045em;line-height:1;margin:1.05rem 0 0;max-width:52rem}.intro span,.systems header span{color:var(--muted);font-size:clamp(1rem,1.5vw,1.18rem);line-height:1.55;margin-top:1rem;max-width:43rem}.offer{background:rgba(244,244,241,.025);border:1px solid rgba(244,244,241,.1);border-radius:1.55rem;display:grid;grid-template-columns:minmax(0,1fr) minmax(0,1fr);margin-top:clamp(1.4rem,3vw,2rem);min-height:clamp(34rem,56vw,43rem);overflow:hidden}.offer.reversed .offer-media{order:2}.offer.reversed .offer-copy{order:1}.offer-media{display:grid;place-items:center;min-height:100%;padding:clamp(1.2rem,3vw,2.4rem);background:radial-gradient(circle at 44% 28%,rgba(255,255,255,.08),transparent 20rem),linear-gradient(135deg,rgba(255,255,255,.11),rgba(255,255,255,.025))}.browser{width:min(100%,34rem);overflow:hidden;border:1px solid rgba(244,244,241,.16);border-radius:1.1rem;background:rgba(9,9,8,.72);backdrop-filter:blur(18px)}.browser-bar{min-height:3rem;padding:0 1rem;border-bottom:1px solid rgba(244,244,241,.11);display:flex;align-items:center;gap:.42rem}.browser-bar i{width:.62rem;height:.62rem;border-radius:50%;background:rgba(244,244,241,.32)}.browser-bar strong{margin-left:.65rem;color:rgba(244,244,241,.64);font-size:.86rem;font-weight:500}.scene{min-height:clamp(24rem,36vw,31rem);padding:clamp(1.2rem,3vw,2rem);position:relative;overflow:hidden}.input-card,.row,.dash-head,.metric,.decision-brief,.option{border:1px solid rgba(244,244,241,.1);border-radius:.9rem;background:rgba(244,244,241,.055)}.input-card{display:grid;gap:.35rem;padding:1rem;opacity:0;transform:translateY(10px)}.input-card span,.dash-head span,.metric span,.decision-brief span{color:var(--soft);font-family:var(--mono);font-size:.68rem;letter-spacing:.14em;text-transform:uppercase}.input-card strong,.dash-head strong,.metric strong,.decision-brief strong{color:var(--ink);font-size:.9rem;font-weight:520}.cursor{position:absolute;right:18%;top:5.2rem;width:.72rem;height:.72rem;border-radius:50%;background:var(--ink);opacity:0}.rows{display:grid;gap:.5rem;margin-top:1.1rem}.row{display:flex;justify-content:space-between;gap:1rem;padding:.82rem .9rem;color:var(--muted);font-size:.9rem;opacity:0;transform:translateY(10px);transition:opacity .72s var(--ease),transform .72s var(--ease);transition-delay:calc(var(--row)*.12s + .46s)}.row strong{color:var(--ink);font-weight:520;text-align:right}.good strong{color:#f2f2ef}.warn strong{color:#d7d7d2}.hot strong{color:#fff}.played .input-card,.played .row{opacity:1;transform:translateY(0)}.played .input-card{border-color:rgba(244,244,241,.34);transition:opacity .7s var(--ease),transform .7s var(--ease),border-color .7s var(--ease)}.played .cursor{animation:cursor-click 1.6s var(--ease) .24s both}.dash-head{display:flex;justify-content:space-between;gap:1rem;padding:1rem;opacity:0;transform:translateY(10px);animation:rise .75s var(--ease) .2s forwards}.dashboard{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:.7rem;margin-top:.85rem}.donut{grid-row:span 2;min-height:15rem;border-radius:.9rem;display:grid;place-items:center;text-align:center;background:conic-gradient(var(--ink) 0 42%,rgba(244,244,241,.54) 42% 70%,rgba(244,244,241,.28) 70% 88%,rgba(244,244,241,.1) 88%);color:#050505;box-shadow:inset 0 0 0 38px rgba(9,9,8,.96)}.donut b{font-size:3rem;color:var(--ink)}.donut span{grid-row:2;color:var(--muted);font-family:var(--mono);font-size:.68rem;text-transform:uppercase;letter-spacing:.12em}.legend,.metric{background:rgba(244,244,241,.055);border:1px solid rgba(244,244,241,.1);border-radius:.9rem;padding:1rem}.legend{display:grid;gap:.65rem}.legend span{display:flex;justify-content:space-between;gap:.8rem;color:var(--muted);font-size:.85rem}.legend i{display:inline-block;width:.55rem;height:.55rem;border-radius:50%;background:var(--ink);margin-right:.35rem}.metric{display:grid;gap:.4rem;opacity:0;transform:translateY(10px);animation:rise .75s var(--ease) .45s forwards}.decision{display:grid;gap:.85rem}.decision-brief{display:grid;gap:.7rem;padding:1rem}.decision-brief div{display:flex;gap:.45rem;flex-wrap:wrap}.decision-brief i{border:1px solid rgba(244,244,241,.12);border-radius:999px;padding:.32rem .52rem;color:var(--muted);font-style:normal;font-family:var(--mono);font-size:.65rem;text-transform:uppercase}.options{display:grid;gap:.65rem}.option{display:grid;gap:.45rem;padding:.9rem;opacity:0;transform:translateY(10px);animation:rise .72s var(--ease) calc(var(--row)*.16s + .22s) forwards}.option strong{font-size:.95rem}.option em{justify-self:end;margin-top:-1.8rem;border:1px solid rgba(244,244,241,.18);border-radius:999px;padding:.22rem .5rem;color:var(--ink);font-style:normal;font-family:var(--mono);font-size:.62rem;text-transform:uppercase}.option span{color:var(--muted);font-size:.82rem}.option b{height:.35rem;border-radius:999px;background:linear-gradient(90deg,var(--ink) calc(var(--fit)*1%),rgba(244,244,241,.12) 0)}.offer-copy{display:grid;align-content:center;padding:clamp(1.5rem,4vw,3.4rem)}.offer-copy h3{font-size:clamp(2rem,4.6vw,4.4rem);font-weight:430;letter-spacing:-.045em;line-height:1;margin:1rem 0 1rem}.offer-copy p{color:var(--muted);font-size:1.05rem;line-height:1.65;margin:.8rem 0 0}.offer-copy .bottom{color:var(--soft)}.offer-copy ul{display:flex;gap:.55rem;flex-wrap:wrap;padding:0;margin:1.4rem 0 0;list-style:none}.offer-copy li{border:1px solid rgba(244,244,241,.14);border-radius:999px;padding:.45rem .7rem;color:var(--muted);font-family:var(--mono);font-size:.68rem;letter-spacing:.1em;text-transform:uppercase}.systems{padding:clamp(4.5rem,9vw,8rem) 0}.systems header,.intelligence header{display:grid;justify-items:center;text-align:center;margin-bottom:clamp(1.8rem,4vw,3rem)}.tabs{display:flex;justify-content:center;flex-wrap:wrap;gap:.45rem;margin-bottom:1rem}.tabs button,.stepper button{border:1px solid rgba(244,244,241,.12);border-radius:.85rem;background:rgba(244,244,241,.035);color:var(--muted);min-height:2.6rem;padding:0 .9rem;font:inherit;cursor:pointer;transition:background .18s ease,color .18s ease,border-color .18s ease}.tabs button.active,.tabs button:hover,.stepper button:hover{background:rgba(244,244,241,.11);border-color:rgba(244,244,241,.28);color:var(--ink)}.stepper{display:none}.feature{display:grid;grid-template-columns:minmax(0,1.05fr) minmax(0,.95fr);gap:1px;border:1px solid rgba(244,244,241,.1);border-radius:1.35rem;overflow:hidden;background:rgba(244,244,241,.1)}.feature-art,.feature-copy{background:rgba(9,9,8,.86);padding:clamp(1.3rem,3vw,2.2rem)}.feature-art{display:grid;place-items:center;min-height:30rem;background:radial-gradient(circle at 50% 20%,rgba(255,255,255,.1),transparent 18rem),rgba(244,244,241,.025)}.art-panel{width:min(100%,30rem);border:1px solid rgba(244,244,241,.14);border-radius:1.1rem;background:rgba(5,5,4,.7);padding:1.2rem}.art-panel>span,.feature-copy>p{color:var(--soft);font-family:var(--mono);font-size:.68rem;letter-spacing:.14em;text-transform:uppercase}.art-panel>strong{display:block;margin-top:.65rem;font-size:1.1rem;font-weight:520}.cert-board{display:grid;gap:.75rem;margin-top:1.2rem}.score{display:grid;place-items:center;text-align:center;min-height:12rem;border:1px solid rgba(244,244,241,.1);border-radius:.95rem;background:rgba(244,244,241,.055)}.score span,.score small,.track span{color:var(--muted);font-family:var(--mono);font-size:.68rem;text-transform:uppercase;letter-spacing:.12em}.score b{font-size:2.2rem;font-weight:520}.track{display:grid;gap:.45rem}.track i{height:.55rem;border-radius:999px;background:linear-gradient(90deg,var(--ink) 78%,rgba(244,244,241,.13) 0)}.skills{display:flex;gap:.45rem;flex-wrap:wrap}.skills span{border:1px solid rgba(244,244,241,.12);border-radius:999px;padding:.35rem .55rem;color:var(--muted);font-size:.72rem}.feature-copy{display:grid;align-content:center}.feature-copy h3{font-size:clamp(2rem,4.3vw,4.1rem);font-weight:430;letter-spacing:-.045em;line-height:1;margin:.9rem 0}.feature-copy>span{color:var(--muted);font-size:1.05rem;line-height:1.65}.feature-copy dl{display:grid;gap:.8rem;margin:1.4rem 0 0}.feature-copy div{border-top:1px solid rgba(244,244,241,.12);padding-top:.8rem}.feature-copy dt{color:var(--soft);font-family:var(--mono);font-size:.68rem;letter-spacing:.14em;text-transform:uppercase}.feature-copy dd{margin:.32rem 0 0;color:var(--muted)}.intelligence{padding:clamp(3rem,7vw,6rem) 0}.intelligence header{grid-template-columns:1fr auto;text-align:left;justify-items:start;align-items:end}.intelligence header p{grid-column:1/-1}.intelligence header h2{margin-top:0}.note-grid{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));border-left:1px solid rgba(244,244,241,.12);border-top:1px solid rgba(244,244,241,.12)}.note-grid a{min-height:16rem;padding:1.4rem;border-right:1px solid rgba(244,244,241,.12);border-bottom:1px solid rgba(244,244,241,.12);background:rgba(244,244,241,.02);transition:background .22s var(--ease),border-color .22s var(--ease),transform .22s var(--ease)}.note-grid a:hover{background:rgba(244,244,241,.08);border-color:rgba(244,244,241,.28);transform:translateY(-2px)}.note-grid span{color:var(--soft);font-family:var(--mono);font-size:.7rem;letter-spacing:.11em;text-transform:uppercase}.note-grid h3{font-size:clamp(1.18rem,2vw,1.55rem);letter-spacing:-.025em;line-height:1.1;margin:1rem 0 0}.note-grid p{color:var(--muted);font-size:.95rem;line-height:1.55}.join{position:relative;padding:clamp(4rem,9vw,8rem) 0;text-align:center;overflow:hidden}.join h2{font-size:clamp(3rem,7vw,7rem);font-weight:430;letter-spacing:-.055em;line-height:.95;margin:0 auto;max-width:58rem}.join p{color:var(--muted);font-size:1.15rem;margin:1.2rem auto 1.8rem;max-width:42rem}.glow{position:absolute;inset:12% 5% auto;height:24rem;background:radial-gradient(ellipse at center,rgba(244,244,241,.12),transparent 68%);filter:blur(18px);pointer-events:none}.gate{height:9rem;margin:3rem auto 0;max-width:54rem;border:1px solid rgba(244,244,241,.12);border-radius:1.35rem;background:linear-gradient(180deg,rgba(244,244,241,.08),rgba(244,244,241,.02));display:grid;grid-template-columns:repeat(4,1fr);overflow:hidden}.gate span{border-left:1px solid rgba(244,244,241,.12);transform:skewX(-12deg)}.gate span:first-child{border-left:0}.footer{border-top:1px solid rgba(244,244,241,.12);padding:2rem 0;color:var(--muted)}.footer .frame{display:flex;justify-content:space-between;gap:1.5rem;flex-wrap:wrap}.footer a{color:var(--ink)}[data-reveal]{opacity:0;transform:translateY(18px);transition:opacity .8s var(--ease),transform .8s var(--ease)}[data-reveal].revealed{opacity:1;transform:none}.article-page{min-height:100vh;padding:9rem 1.5rem 3rem}.read-progress{position:fixed;top:0;left:0;width:0;height:3px;background:var(--ink);z-index:50}.article-shell{width:min(100%,56rem);margin:0 auto;text-align:center}.article-shell h1{font-size:clamp(3.4rem,8vw,7rem);font-weight:430;line-height:.92;letter-spacing:-.055em;margin:0}.article-meta{display:flex;justify-content:center;gap:.55rem;flex-wrap:wrap;margin:1.4rem auto 2.5rem;color:var(--muted);font-family:var(--mono);font-size:.68rem;text-transform:uppercase;letter-spacing:.12em}.article-meta span{border:1px solid rgba(244,244,241,.12);border-radius:999px;padding:.45rem .65rem;background:rgba(244,244,241,.035)}.article-body{position:relative;margin:0 auto;padding:clamp(1.5rem,4vw,3rem);border:1px solid rgba(244,244,241,.12);border-radius:1.35rem;background:rgba(244,244,241,.035);text-align:left}.article-body:before{content:"";position:absolute;inset:1rem;border:1px solid rgba(244,244,241,.055);border-radius:1rem;pointer-events:none}.article-body .lede{color:var(--soft);font-size:clamp(1.25rem,2vw,1.75rem);line-height:1.35;text-align:center;margin-top:0}.article-body h2{margin:2.4rem 0 .8rem;text-align:center;font-size:clamp(1.6rem,3vw,2.45rem);font-weight:450;letter-spacing:-.035em}.article-body p{color:var(--muted);font-size:1.05rem;line-height:1.75;margin:.85rem auto;max-width:42rem}.article-body a{color:var(--ink);border-bottom:1px solid rgba(244,244,241,.5)}.article-body details{max-width:42rem;margin:2rem auto 0;padding:1rem;border:1px solid rgba(244,244,241,.12);border-radius:1rem;background:rgba(244,244,241,.035)}.article-body summary{cursor:pointer;color:var(--ink);font-family:var(--mono);font-size:.74rem;letter-spacing:.12em;text-transform:uppercase}.updated{text-align:center;font-family:var(--mono);font-size:.8rem;text-transform:uppercase;letter-spacing:.1em}@keyframes rise{to{opacity:1;transform:translateY(0)}}@keyframes cursor-click{0%{opacity:0;transform:translate(30px,-20px) scale(.8)}38%{opacity:1;transform:translate(0,0) scale(1)}58%{transform:translate(0,0) scale(.62)}78%{transform:translate(0,0) scale(1)}100%{opacity:0;transform:translate(-8px,8px) scale(.9)}}@media(max-width:920px){.nav-links,.nav-cta{display:none}.menu-button{display:inline-flex;width:3.05rem;height:3.05rem;border:1px solid rgba(244,244,241,.16);border-radius:.92rem;background:rgba(244,244,241,.06);position:relative}.menu-button span{position:absolute;left:.9rem;right:.9rem;height:1.5px;background:var(--ink);transition:transform .22s var(--ease),opacity .18s}.menu-button span:first-child{top:1.05rem}.menu-button span:nth-child(2){top:1.5rem}.menu-button span:nth-child(3){top:1.95rem}.nav-shell.open .menu-button span:first-child{top:1.5rem;transform:rotate(45deg)}.nav-shell.open .menu-button span:nth-child(2){opacity:0}.nav-shell.open .menu-button span:nth-child(3){top:1.5rem;transform:rotate(-45deg)}.mobile-menu{display:none;position:absolute;left:0;right:0;top:calc(100% + .55rem);padding:.65rem;border:1px solid rgba(244,244,241,.14);border-radius:1.25rem;background:rgba(8,8,7,.96);backdrop-filter:blur(24px)}.nav-shell.open .mobile-menu{display:grid}.mobile-menu a{min-height:2.85rem;padding:0 .85rem;border-radius:.82rem;color:rgba(244,244,241,.75);display:flex;align-items:center}.mobile-menu a:hover{background:rgba(244,244,241,.08);color:var(--ink)}}@media(min-width:921px){.mobile-menu{display:none}}@media(max-width:820px){.offer,.feature{grid-template-columns:1fr}.offer.reversed .offer-media,.offer.reversed .offer-copy{order:initial}.note-grid{grid-template-columns:1fr}.intelligence header{grid-template-columns:1fr;text-align:center;justify-items:center}.tabs{display:none}.stepper{display:flex;align-items:center;justify-content:space-between;gap:1rem;margin-bottom:1rem}.dashboard{grid-template-columns:1fr}.donut{grid-row:auto}.source-pill{align-items:flex-start;border-radius:1rem;flex-direction:column;text-align:left}.article-page{padding-top:7rem}}@media(max-width:560px){.frame{width:min(100% - 1.5rem,72rem)}.nav-shell{width:min(100% - 1.5rem,58.5rem);border-radius:1.35rem;min-height:4rem;padding:.45rem .5rem .45rem .9rem}.hero{padding-top:8rem}.hero h1{font-size:clamp(2.45rem,11vw,3.9rem)}.offer-media,.offer-copy,.feature-art,.feature-copy{padding:1rem}.scene{min-height:22rem}.intro h2,.systems header h2,.intelligence header h2{font-size:clamp(2.4rem,12vw,3.7rem)}.join h2{font-size:clamp(2.7rem,13vw,4.2rem)}.article-shell h1{font-size:clamp(3rem,14vw,4.6rem)}.article-body{padding:1.2rem}.article-body:before{display:none}}@media(prefers-reduced-motion:reduce){*,*:before,*:after{animation:none!important;scroll-behavior:auto!important;transition:none!important}[data-reveal]{opacity:1;transform:none}}
</style></head><body><header class="site-header" data-header><div class="nav-shell" data-nav><a class="brand" href="/" aria-label="Raze Posting home"><span class="brand-mark">R</span><span class="brand-name">Raze<br>Posting</span></a><nav class="nav-links"><a href="/privacy">Privacy</a><a href="/terms">Terms</a><a href="/data-deletion">Data deletion</a></nav><a class="nav-cta" href="mailto:` + legalContactEmail + `">Contact</a><button class="menu-button" type="button" data-menu aria-label="Toggle navigation"><span></span><span></span><span></span></button><div class="mobile-menu"><a href="/privacy">Privacy</a><a href="/terms">Terms</a><a href="/data-deletion">Data deletion</a><a href="mailto:` + legalContactEmail + `">Contact</a></div></div></header>` + body + `<footer class="footer"><div class="frame"><a href="/">Raze Posting</a><span>Meta operations. Reviewed before publish.</span><a href="mailto:` + legalContactEmail + `">` + legalContactEmail + `</a></div></footer><script>
(function(){
  var phrases = ["keeps launches observable","turns Meta work into systems","keeps batches reviewable","makes retries visible"];
  var phrase = document.querySelector("[data-phrase]");
  var index = 0;
  if (phrase) {
    setInterval(function(){
      phrase.classList.add("swap");
      setTimeout(function(){
        index = (index + 1) % phrases.length;
        phrase.textContent = phrases[index];
        phrase.classList.remove("swap");
      }, 520);
    }, 2600);
  }
  var header = document.querySelector("[data-header]");
  var lastY = window.scrollY;
  window.addEventListener("scroll", function(){
    var y = window.scrollY;
    if (header) {
      header.classList.toggle("hidden", y > lastY && y > 220);
    }
    lastY = y;
    var progress = document.querySelector(".read-progress");
    if (progress) {
      var max = document.documentElement.scrollHeight - window.innerHeight;
      progress.style.width = (max > 0 ? y / max * 100 : 0) + "%";
    }
  }, {passive:true});
  var nav = document.querySelector("[data-nav]");
  var menu = document.querySelector("[data-menu]");
  if (menu && nav) {
    menu.addEventListener("click", function(){ nav.classList.toggle("open"); });
  }
  var reveal = new IntersectionObserver(function(entries){
    entries.forEach(function(entry){
      if (entry.isIntersecting) {
        entry.target.classList.add("revealed");
        reveal.unobserve(entry.target);
      }
    });
  }, {threshold:.13});
  document.querySelectorAll("[data-reveal]").forEach(function(el){ reveal.observe(el); });
  var panels = [
    ["Business access","Approved assets enter the workspace with scope attached.","Keep Meta access explicit before any operation starts.","Raze Posting works from authorized users and the assets they are allowed to manage. The workspace is useful because it starts with clear boundaries.","Connection records, business IDs, ad account IDs, token scope.","Operators know which assets are in scope before a batch is prepared.","Verified","Business, ad accounts, scope"],
    ["Campaign evidence","Campaign state becomes a shared review surface.","Review campaigns with enough context to make the next move.","Campaigns, ad sets, ads, creatives, and insight snapshots are pulled into a consistent operating view before the team changes anything.","Campaign hierarchy, performance windows, creative references, account activity.","The team can discuss the work from evidence instead of screenshots.","Synced","Campaign, ad set, ad, insight"],
    ["Campaign operations","Campaign changes are prepared and checked.","Treat ad delivery changes as controlled operational work.","The service prepares authorized campaign operations and records the outcome instead of hiding changes in a browser tab.","Campaign IDs, creative references, approval state, delivery status.","Campaign work remains attributable, visible, and reviewable.","Queued","Campaigns, ads, media, approvals"],
    ["Media pipeline","Uploaded assets stay attached to the operation.","Keep files, accounts, and publish jobs from drifting apart.","Media handling tracks which assets belong to which account, whether they uploaded successfully, and which jobs depend on them.","Local files, upload state, account mapping, job dependencies.","Operators can see whether the media layer is ready before a batch starts.","Tracked","Files, uploads, mapping, queue"],
    ["Job recovery","The aftermath is part of the product.","Make retries and failures concrete enough to fix.","Background jobs expose status, rate-limit pauses, retries, errors, and completion records so follow-up is precise.","Worker status, retry windows, error messages, completion records.","Failures become recoverable events rather than mystery gaps.","Visible","Jobs, retries, errors, outcomes"]
  ];
  var active = 0;
  function setPanel(i){
    active = (i + panels.length) % panels.length;
    var p = panels[active];
    document.querySelectorAll("[data-tab]").forEach(function(btn){
      btn.classList.toggle("active", Number(btn.dataset.tab) === active);
    });
    var pairs = [["[data-tab-label]",p[0]],["[data-art-kicker]",p[0]],["[data-art-title]",p[1]],["[data-panel-kicker]",p[0]],["[data-panel-title]",p[2]],["[data-panel-copy]",p[3]],["[data-panel-layer]",p[4]],["[data-panel-result]",p[5]],["[data-art-score]",p[6]],["[data-art-track]",p[7]]];
    pairs.forEach(function(pair){
      var el = document.querySelector(pair[0]);
      if (el) el.textContent = pair[1];
    });
  }
  document.querySelectorAll("[data-tab]").forEach(function(btn){
    btn.addEventListener("click", function(){ setPanel(Number(btn.dataset.tab)); });
  });
  var prev = document.querySelector("[data-tab-prev]");
  var next = document.querySelector("[data-tab-next]");
  if (prev) prev.addEventListener("click", function(){ setPanel(active - 1); });
  if (next) next.addEventListener("click", function(){ setPanel(active + 1); });
})();
</script></body></html>`
	c.Set("Content-Type", "text/html; charset=utf-8")
	c.Set("Cache-Control", "public, max-age=300")
	return c.Status(http.StatusOK).SendString(page)
}
