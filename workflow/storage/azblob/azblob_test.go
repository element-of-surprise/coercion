package azblob

import (
	"testing"

	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow/context"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

// fakeCred is a non-nil azcore.TokenCredential for exercising Args.validate without a real account.
type fakeCred struct{}

func (fakeCred) GetToken(ctx context.Context, opts policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{}, nil
}

func TestValidate(t *testing.T) {
	t.Parallel()

	reg := registry.New()

	// base returns a fully valid Args that each case mutates in exactly one place.
	base := func() Args {
		return Args{
			Prefix:        "coercion",
			Endpoint:      "https://account.blob.core.windows.net",
			Cred:          fakeCred{},
			Reg:           reg,
			RetentionDays: 2,
		}
	}

	tests := []struct {
		name    string
		args    func() Args
		wantErr bool
	}{
		{
			name: "Success: all fields valid with the minimum retention",
			args: base,
		},
		{
			name:    "Error: retentionDays of 1 cannot cover a midnight boundary",
			args:    func() Args { a := base(); a.RetentionDays = 1; return a },
			wantErr: true,
		},
		{
			name:    "Error: retentionDays of 0",
			args:    func() Args { a := base(); a.RetentionDays = 0; return a },
			wantErr: true,
		},
		{
			name:    "Error: prefix is empty",
			args:    func() Args { a := base(); a.Prefix = ""; return a },
			wantErr: true,
		},
		{
			name:    "Error: endpoint is empty",
			args:    func() Args { a := base(); a.Endpoint = ""; return a },
			wantErr: true,
		},
		{
			name:    "Error: credential is nil",
			args:    func() Args { a := base(); a.Cred = nil; return a },
			wantErr: true,
		},
		{
			name:    "Error: registry is nil",
			args:    func() Args { a := base(); a.Reg = nil; return a },
			wantErr: true,
		},
	}

	for _, test := range tests {
		args := test.args()
		err := args.validate(t.Context())
		switch {
		case err == nil && test.wantErr:
			t.Errorf("TestValidate(%s): got err == nil, want err != nil", test.name)
			continue
		case err != nil && !test.wantErr:
			t.Errorf("TestValidate(%s): got err == %s, want err == nil", test.name, err)
			continue
		case err != nil:
			continue
		}
	}
}
