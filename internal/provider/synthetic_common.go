package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listdefault"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// syntheticChecksPath is the public API collection every synthetic check
// resource manages (atatus-server api/lib/routes/public-api.js).
const syntheticChecksPath = "/v2/synthetic/checks"

// defaultLocations is the "locations" schema default. Declaring it in the
// schema (rather than only filling it in when the payload is built) keeps a
// config without "locations" from planning an update on every run.
var defaultLocations = listdefault.StaticValue(types.ListValueMust(types.StringType, []attr.Value{types.StringValue("US_NEW_YORK")}))

// NotificationChannelSettings is the part of a channel's settings the
// provider reads. GET /v2/channels does not return settings today, so
// Recipient is usually empty and the channel name is used instead.
type NotificationChannelSettings struct {
	Recipient string `json:"recipient,omitempty"`
	URL       string `json:"url,omitempty"`
}

// NotificationChannel is one row of GET /v2/channels.
type NotificationChannel struct {
	ID        string                      `json:"_id"`
	Name      string                      `json:"name"`
	Type      string                      `json:"type"`
	AccountID string                      `json:"accountId"`
	Settings  NotificationChannelSettings `json:"settings"`
}

// syntheticBase is embedded by every synthetic check resource. It holds the
// shared API client and the plumbing the six resources used to repeat:
// provider configuration, list attribute helpers, notification channel
// resolution, the check CRUD calls and import by id.
type syntheticBase struct {
	client *apiClient
}

// Configure takes the shared API client from the provider.
func (b *syntheticBase) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	cfg, ok := req.ProviderData.(*AtatusConfig)
	if !ok {
		resp.Diagnostics.AddError("Configuration Error", fmt.Sprintf("Expected *AtatusConfig, got %T", req.ProviderData))
		return
	}
	b.client = cfg.Client
}

// ImportState adopts an existing check by its id; Read fills in the rest.
func (b *syntheticBase) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// stringsFromList returns the known string elements of a list attribute, or
// fallback when the list is null, unknown or empty. Empty strings are kept,
// as the resources always sent them.
func stringsFromList(ctx context.Context, list types.List, fallback []string) ([]string, diag.Diagnostics) {
	var diags diag.Diagnostics
	if list.IsNull() || list.IsUnknown() {
		return fallback, diags
	}
	var elements []types.String
	diags.Append(list.ElementsAs(ctx, &elements, false)...)
	if diags.HasError() {
		return fallback, diags
	}
	values := make([]string, 0, len(elements))
	for _, el := range elements {
		if !el.IsNull() && !el.IsUnknown() {
			values = append(values, el.ValueString())
		}
	}
	if len(values) == 0 {
		return fallback, diags
	}
	return values, diags
}

// getLocations defaults to US_NEW_YORK, as the checks always have.
func (b *syntheticBase) getLocations(ctx context.Context, list types.List) ([]string, diag.Diagnostics) {
	return stringsFromList(ctx, list, []string{"US_NEW_YORK"})
}

func (b *syntheticBase) getTags(ctx context.Context, list types.List) ([]string, diag.Diagnostics) {
	return stringsFromList(ctx, list, []string{})
}

func (b *syntheticBase) getChannelIDs(ctx context.Context, list types.List) ([]string, diag.Diagnostics) {
	return stringsFromList(ctx, list, []string{})
}

func (b *syntheticBase) getEmails(ctx context.Context, list types.List) ([]string, diag.Diagnostics) {
	return stringsFromList(ctx, list, []string{})
}

func (b *syntheticBase) fetchNotificationChannels(ctx context.Context) ([]NotificationChannel, error) {
	c, err := b.api()
	if err != nil {
		return nil, err
	}
	return c.notificationChannels(ctx)
}

// channelEmail is the address an EMAIL channel stands for: its recipient
// when the API returns one, otherwise its name.
func channelEmail(ch NotificationChannel) string {
	if ch.Settings.Recipient != "" {
		return ch.Settings.Recipient
	}
	return ch.Name
}

// resolveEmailsToChannelIDs maps each address to the id of the account's
// EMAIL channel for it, and errors on an address with no channel.
func (b *syntheticBase) resolveEmailsToChannelIDs(ctx context.Context, emails []string) ([]string, error) {
	if len(emails) == 0 {
		return nil, nil
	}

	channels, err := b.fetchNotificationChannels(ctx)
	if err != nil {
		return nil, err
	}

	emailToID := make(map[string]string)
	var availableEmails []string
	for _, ch := range channels {
		if !strings.EqualFold(ch.Type, "EMAIL") {
			continue
		}
		if rec := channelEmail(ch); rec != "" {
			emailToID[strings.ToLower(strings.TrimSpace(rec))] = ch.ID
			availableEmails = append(availableEmails, rec)
		}
	}

	var resolvedIDs []string
	for _, email := range emails {
		id, ok := emailToID[strings.ToLower(strings.TrimSpace(email))]
		if !ok {
			if len(availableEmails) > 0 {
				return nil, fmt.Errorf("no email notification channel found for '%s' in Atatus account. Please create this notification channel in Atatus first. Available email channels in account: [%s]", email, strings.Join(availableEmails, ", "))
			}
			return nil, fmt.Errorf("no email notification channel found for '%s' in Atatus account. Please create this notification channel in Atatus first", email)
		}
		resolvedIDs = append(resolvedIDs, id)
	}
	return resolvedIDs, nil
}

// resolveChannelIDsToEmails splits a check's channel ids into the addresses
// of its EMAIL channels and the ids of every other channel (including ids
// the account no longer has).
func (b *syntheticBase) resolveChannelIDsToEmails(ctx context.Context, channelIDs []string) ([]string, []string, error) {
	if len(channelIDs) == 0 {
		return nil, nil, nil
	}

	channels, err := b.fetchNotificationChannels(ctx)
	if err != nil {
		return nil, nil, err
	}

	idToChannel := make(map[string]NotificationChannel, len(channels))
	for _, ch := range channels {
		idToChannel[ch.ID] = ch
	}

	var emails, nonEmailChannelIDs []string
	for _, id := range channelIDs {
		ch, ok := idToChannel[id]
		if ok && strings.EqualFold(ch.Type, "EMAIL") {
			if rec := channelEmail(ch); rec != "" {
				emails = append(emails, rec)
			}
			continue
		}
		nonEmailChannelIDs = append(nonEmailChannelIDs, id)
	}
	return emails, nonEmailChannelIDs, nil
}

// errNotConfigured is returned when the provider could not be configured for
// this run (its api_key or endpoint is only known after apply).
var errNotConfigured = fmt.Errorf("the Atatus provider is not configured yet: its api_key or endpoint depends on a value that is only known after apply")

func (b *syntheticBase) api() (*apiClient, error) {
	if b.client == nil {
		return nil, errNotConfigured
	}
	return b.client, nil
}

func checkPath(id string) string {
	return syntheticChecksPath + "/" + url.PathEscape(id)
}

// createCheck POSTs a new check and decodes the created document into out.
func (b *syntheticBase) createCheck(ctx context.Context, payload, out any) error {
	c, err := b.api()
	if err != nil {
		return err
	}
	return c.doJSON(ctx, "POST", syntheticChecksPath, payload, out)
}

// getCheck reads one check into out. A missing check returns an error for
// which IsNotFound is true. A check of another kind than subtype (an SSL
// check's id imported as atatus_http_check) is an error, so it is never
// read, and later written, as the wrong kind.
func (b *syntheticBase) getCheck(ctx context.Context, id, subtype string, out any) error {
	c, err := b.api()
	if err != nil {
		return err
	}
	var raw json.RawMessage
	if err := c.doJSON(ctx, "GET", checkPath(id), nil, &raw); err != nil {
		return err
	}
	if len(raw) == 0 {
		return nil
	}
	var head struct {
		Subtype string `json:"subtype"`
	}
	if err := json.Unmarshal(raw, &head); err == nil && head.Subtype != "" && head.Subtype != subtype {
		return fmt.Errorf("check %s is a %q check, not a %q check: manage it with the matching atatus_*_check resource", id, head.Subtype, subtype)
	}
	return json.Unmarshal(raw, out)
}

// patchCheck updates a check. The server has no PUT for checks, and its
// PATCH is a partial $set, so the provider always sends the full payload.
func (b *syntheticBase) patchCheck(ctx context.Context, id string, payload any) error {
	c, err := b.api()
	if err != nil {
		return err
	}
	return c.doJSON(ctx, "PATCH", checkPath(id), payload, nil)
}

// deleteCheck removes a check. A check that is already gone is not an error.
func (b *syntheticBase) deleteCheck(ctx context.Context, id string) error {
	c, err := b.api()
	if err != nil {
		return err
	}
	err = c.doJSON(ctx, "DELETE", checkPath(id), nil, nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}

// readChannels fills emails and channel_ids from a check's channel ids. When
// the channel lookup fails it keeps the previous values and warns, rather
// than silently leaving stale values in state.
func (b *syntheticBase) readChannels(ctx context.Context, channelIDs []string, emails, nonEmail *types.List, diags *diag.Diagnostics) {
	if len(channelIDs) == 0 {
		*emails = types.ListNull(types.StringType)
		*nonEmail = types.ListNull(types.StringType)
		return
	}

	resolvedEmails, otherIDs, err := b.resolveChannelIDsToEmails(ctx, channelIDs)
	if err != nil {
		diags.AddWarning(
			"Notification Channels Not Refreshed",
			"Could not read the account's notification channels, so emails and channel_ids keep their previous values: "+err.Error(),
		)
		return
	}

	*emails = stringListOrNull(ctx, resolvedEmails, diags)
	*nonEmail = stringListOrNull(ctx, otherIDs, diags)
}

// stringListOrNull returns a list value, or null for an empty slice.
func stringListOrNull(ctx context.Context, values []string, diags *diag.Diagnostics) types.List {
	if len(values) == 0 {
		return types.ListNull(types.StringType)
	}
	list, d := types.ListValueFrom(ctx, types.StringType, values)
	diags.Append(d...)
	return list
}
