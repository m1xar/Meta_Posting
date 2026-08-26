// Field vocabularies for the launcher form.
//
// Kept beside the form rather than fetched: these are Meta's enums, they
// change with an API version rather than per account, and a dropdown that
// waits on a round trip to render its options is worse than one that does not.

export const OBJECTIVES = [
  ['OUTCOME_SALES', 'Sales'],
  ['OUTCOME_LEADS', 'Leads'],
  ['OUTCOME_TRAFFIC', 'Traffic'],
  ['OUTCOME_ENGAGEMENT', 'Engagement'],
  ['OUTCOME_AWARENESS', 'Awareness'],
  ['OUTCOME_APP_PROMOTION', 'App promotion'],
];

export const OPTIMIZATION_GOALS = [
  ['', 'Inherit from source ad set'],
  ['OFFSITE_CONVERSIONS', 'Conversions'],
  ['LINK_CLICKS', 'Link clicks'],
  ['LANDING_PAGE_VIEWS', 'Landing page views'],
  ['IMPRESSIONS', 'Impressions'],
  ['REACH', 'Reach'],
  ['THRUPLAY', 'ThruPlay'],
  ['LEAD_GENERATION', 'Lead generation'],
  ['VALUE', 'Value'],
];

export const BILLING_EVENTS = [
  ['', 'Inherit from source ad set'],
  ['IMPRESSIONS', 'Impressions'],
  ['LINK_CLICKS', 'Link clicks'],
  ['THRUPLAY', 'ThruPlay'],
];

export const BID_STRATEGIES = [
  ['', 'Meta default'],
  ['LOWEST_COST_WITHOUT_CAP', 'Highest volume'],
  ['LOWEST_COST_WITH_BID_CAP', 'Bid cap'],
  ['COST_CAP', 'Cost cap'],
];

export const CALL_TO_ACTIONS = [
  ['LEARN_MORE', 'Learn more'],
  ['SHOP_NOW', 'Shop now'],
  ['SIGN_UP', 'Sign up'],
  ['SUBSCRIBE', 'Subscribe'],
  ['DOWNLOAD', 'Download'],
  ['GET_OFFER', 'Get offer'],
  ['BOOK_TRAVEL', 'Book now'],
  ['CONTACT_US', 'Contact us'],
  ['APPLY_NOW', 'Apply now'],
  ['PLAY_GAME', 'Play game'],
];

// Meta requires the field even when nothing applies, and getting it wrong is
// a policy problem rather than a delivery one.
export const SPECIAL_CATEGORIES = [
  ['CREDIT', 'Credit'],
  ['EMPLOYMENT', 'Employment'],
  ['HOUSING', 'Housing'],
  ['ISSUES_ELECTIONS_POLITICS', 'Social issues, elections or politics'],
  ['ONLINE_GAMBLING_AND_GAMING', 'Online gambling and gaming'],
];

/** Metrics a guard can watch, grouped so the list stays readable. */
export const GUARD_METRICS = [
  ['Delivery', [
    ['impressions', 'impressions'],
    ['reach', 'reach'],
    ['clicks', 'clicks'],
    ['inline_link_clicks', 'link clicks'],
  ]],
  ['Cost', [
    ['cpc', 'CPC'],
    ['cpm', 'CPM'],
    ['ctr', 'CTR %'],
  ]],
  ['Results', [
    ['actions.landing_page_view', 'landing page views'],
    ['actions.complete_registration', 'registrations'],
    ['actions.lead', 'leads'],
    ['actions.purchase', 'purchases'],
    ['actions.add_to_cart', 'add to cart'],
    ['actions.initiate_checkout', 'checkouts started'],
  ]],
];

/** Metrics where a *higher* number is the bad outcome, so the guard flips. */
export const HIGHER_IS_WORSE = new Set(['cpc', 'cpm']);

export const flatMetrics = () => GUARD_METRICS.flatMap(([, items]) => items);

export const metricLabel = (value) =>
  (flatMetrics().find(([metric]) => metric === value) || [null, value])[1];
