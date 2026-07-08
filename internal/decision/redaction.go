package decision

import "github.com/vibe-agi/hideout/internal/audit"

func RedactPreview(in Preview) Preview {
	out := Preview{
		Summary:         audit.RedactString(in.Summary),
		Diff:            audit.RedactValue("diff", in.Diff),
		UserDataPresent: in.UserDataPresent,
	}
	if in.Facts != nil {
		out.Facts = audit.RedactDetails(in.Facts)
	}
	return out
}

func RedactDecision(in Decision) Decision {
	out := in
	out.Preview = RedactPreview(in.Preview)
	out.Risk = audit.RedactDetails(in.Risk)
	out.ProposedAction = audit.RedactDetails(in.ProposedAction)
	out.ProviderRef = ProviderRef{}
	if out.Claim != nil {
		claim := *out.Claim
		claim.TokenHash = ""
		out.Claim = &claim
	}
	return out
}

func RedactNotice(in Notice) Notice {
	out := in
	out.Preview = RedactPreview(in.Preview)
	out.Payload = audit.RedactDetails(in.Payload)
	return out
}

func RedactResolution(in Resolution) Resolution {
	out := in
	out.Reason = audit.RedactString(out.Reason)
	out.ProviderResult = audit.RedactDetails(out.ProviderResult)
	return out
}
