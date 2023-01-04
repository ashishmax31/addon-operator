package addon

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	addonsv1alpha1 "github.com/openshift/addon-operator/apis/addons/v1alpha1"
	"github.com/openshift/addon-operator/internal/controllers/runtimeoptions/runtimeoptionstest"
	"github.com/openshift/addon-operator/internal/metrics"
	"github.com/openshift/addon-operator/internal/ocm"
	"github.com/openshift/addon-operator/internal/ocm/ocmtest"
	"github.com/openshift/addon-operator/internal/testutil"
)

func TestHandleAddonStatusReporting(t *testing.T) {
	t.Run("noop when ocm client is not initialized", func(t *testing.T) {
		statusReportingOption := &runtimeoptionstest.RuntimeOptionMock{}
		statusReportingOption.On("Enabled").Return(true)
		r := AddonReconciler{
			statusReportingOption: statusReportingOption,
		}
		addon := &addonsv1alpha1.Addon{}
		log := testutil.NewLogger(t)
		err := r.handleOCMAddOnStatusReporting(context.Background(), log, addon)
		require.NoError(t, err)
	})

	t.Run("noop when current addon status is equal to the last reported status", func(t *testing.T) {
		statusReportingOption := &runtimeoptionstest.RuntimeOptionMock{}
		statusReportingOption.On("Enabled").Return(true)
		r := AddonReconciler{
			statusReportingOption: statusReportingOption,
		}
		addon := &addonsv1alpha1.Addon{
			ObjectMeta: metav1.ObjectMeta{
				Name: "addon-1",
			},
			Spec: addonsv1alpha1.AddonSpec{
				CorrelationID: "123",
			},
			Status: addonsv1alpha1.AddonStatus{
				Conditions: []metav1.Condition{
					{
						Type:   addonsv1alpha1.Available,
						Status: metav1.ConditionTrue,
						Reason: addonsv1alpha1.AddonReasonFullyReconciled,
					},
				},
				ReportedStatus: &addonsv1alpha1.OCMAddOnStatus{
					AddonID:       "addon-1",
					CorrelationID: "123",
					StatusConditions: []addonsv1alpha1.AddOnStatusCondition{
						{
							StatusType:  addonsv1alpha1.Available,
							StatusValue: metav1.ConditionTrue,
							Reason:      addonsv1alpha1.AddonReasonFullyReconciled,
						},
					},
				},
			},
		}
		log := testutil.NewLogger(t)
		err := r.handleOCMAddOnStatusReporting(context.Background(), log, addon)
		require.NoError(t, err)
	})

	t.Run("noop when status reporting is disabled", func(t *testing.T) {
		client := testutil.NewClient()
		ocmClient := ocmtest.NewClient()
		statusReportingOption := &runtimeoptionstest.RuntimeOptionMock{}
		statusReportingOption.On("Enabled").Return(false)
		r := AddonReconciler{
			Client:                client,
			ocmClient:             ocmClient,
			statusReportingOption: statusReportingOption,
		}
		addon := &addonsv1alpha1.Addon{
			ObjectMeta: metav1.ObjectMeta{
				Name: "addon-1",
			},
			Spec: addonsv1alpha1.AddonSpec{
				CorrelationID: "123",
			},
			Status: addonsv1alpha1.AddonStatus{
				Conditions: []metav1.Condition{
					{
						Type:   addonsv1alpha1.Available,
						Status: metav1.ConditionTrue,
						Reason: addonsv1alpha1.AddonReasonFullyReconciled,
					},
				},
			},
		}
		log := testutil.NewLogger(t)
		err := r.handleOCMAddOnStatusReporting(context.Background(), log, addon)
		ocmClient.AssertNotCalled(t, mock.Anything)
		require.NoError(t, err)
	})

	t.Run("correctly posts the current addon status when reporting status for the first time", func(t *testing.T) {
		client := testutil.NewClient()
		ocmClient := ocmtest.NewClient()
		recorder := metrics.NewRecorder(false, "asa346546dfew143")
		mockSummary := testutil.NewSummaryMock()
		statusReportingOption := &runtimeoptionstest.RuntimeOptionMock{}
		statusReportingOption.On("Enabled").Return(true)
		recorder.InjectAddonServiceAPIRequestDuration(mockSummary)
		log := testutil.NewLogger(t)
		r := &AddonReconciler{
			Client:                client,
			ocmClient:             ocmClient,
			Recorder:              recorder,
			statusReportingOption: statusReportingOption,
		}

		addon := &addonsv1alpha1.Addon{
			ObjectMeta: metav1.ObjectMeta{
				Name: "addon-1",
			},
			Spec: addonsv1alpha1.AddonSpec{
				CorrelationID: "123",
			},
			Status: addonsv1alpha1.AddonStatus{
				Conditions: []metav1.Condition{
					{
						Type:   addonsv1alpha1.Available,
						Status: metav1.ConditionTrue,
						Reason: addonsv1alpha1.AddonReasonFullyReconciled,
					},
				},
			},
		}
		// setup mock calls
		ocmClient.On("GetAddOnStatus", mock.Anything, "addon-1").
			Return(
				ocm.AddOnStatusResponse{},
				ocm.OCMError{
					StatusCode: http.StatusNotFound,
				},
			)
		ocmClient.On("PostAddOnStatus", mock.Anything, ocm.AddOnStatusPostRequest{
			AddonID:          "addon-1",
			CorrelationID:    "123",
			StatusConditions: mapAddonStatusConditions(addon.Status.Conditions),
		}).Return(
			ocm.AddOnStatusResponse{},
			nil,
		)
		mockSummary.On(
			"Observe", mock.IsType(float64(0)))

		err := r.handleOCMAddOnStatusReporting(context.Background(), log, addon)
		require.NoError(t, err)
		ocmClient.AssertExpectations(t)
		mockSummary.AssertExpectations(t)

		// Assert that the reported status is indeed stored in the addon's status
		// block.
		require.NotNil(t, addon.Status.ReportedStatus)
		require.Equal(t, addon.Spec.CorrelationID, addon.Status.ReportedStatus.CorrelationID)
		require.Equal(t, addon.Name, addon.Status.ReportedStatus.AddonID)
		require.Equal(t, mapAddonStatusConditions(addon.Status.Conditions),
			addon.Status.ReportedStatus.StatusConditions)
	})

	t.Run("outdated reported status, but current status is equal to OCM status", func(t *testing.T) {
		client := testutil.NewClient()
		ocmClient := ocmtest.NewClient()
		recorder := metrics.NewRecorder(false, "asa346546dfew143")
		mockSummary := testutil.NewSummaryMock()
		recorder.InjectAddonServiceAPIRequestDuration(mockSummary)
		statusReportingOption := &runtimeoptionstest.RuntimeOptionMock{}
		statusReportingOption.On("Enabled").Return(true)
		log := testutil.NewLogger(t)
		r := &AddonReconciler{
			Client:                client,
			ocmClient:             ocmClient,
			Recorder:              recorder,
			statusReportingOption: statusReportingOption,
		}

		addon := &addonsv1alpha1.Addon{
			ObjectMeta: metav1.ObjectMeta{
				Name: "addon-1",
			},
			Spec: addonsv1alpha1.AddonSpec{
				CorrelationID: "1234",
			},
			Status: addonsv1alpha1.AddonStatus{
				Conditions: []metav1.Condition{
					{
						Type:   addonsv1alpha1.Available,
						Status: metav1.ConditionTrue,
						Reason: addonsv1alpha1.AddonReasonFullyReconciled,
					},
					{
						Type:   addonsv1alpha1.UpgradeStarted,
						Status: metav1.ConditionTrue,
						Reason: addonsv1alpha1.AddonReasonUpgradeStarted,
					},
				},
				ReportedStatus: &addonsv1alpha1.OCMAddOnStatus{
					AddonID:       "addon-1",
					CorrelationID: "123",
					StatusConditions: []addonsv1alpha1.AddOnStatusCondition{
						{
							StatusType:  addonsv1alpha1.Available,
							StatusValue: metav1.ConditionTrue,
							Reason:      addonsv1alpha1.AddonReasonFullyReconciled,
						},
					},
				},
			},
		}
		// setup mock calls
		ocmClient.On("GetAddOnStatus", mock.Anything, "addon-1").
			Return(
				ocm.AddOnStatusResponse{
					AddonID:       "addon-1",
					CorrelationID: "1234",
					StatusConditions: []addonsv1alpha1.AddOnStatusCondition{
						{
							StatusType:  addonsv1alpha1.Available,
							StatusValue: metav1.ConditionTrue,
							Reason:      addonsv1alpha1.AddonReasonFullyReconciled,
						},
						{
							StatusType:  addonsv1alpha1.UpgradeStarted,
							StatusValue: metav1.ConditionTrue,
							Reason:      addonsv1alpha1.AddonReasonUpgradeStarted,
						},
					},
				},
				nil,
			)
		mockSummary.On(
			"Observe", mock.IsType(float64(0)))

		// No POST or PATCH calls made to OCM as the status in OCM
		// is the same as in the current in cluster addon status.
		ocmClient.AssertNotCalled(t, "PostAddOnStatus")
		ocmClient.AssertNotCalled(t, "PatchAddOnStatus")
		err := r.handleOCMAddOnStatusReporting(context.Background(), log, addon)
		require.NoError(t, err)
		ocmClient.AssertExpectations(t)
		mockSummary.AssertExpectations(t)

		// Assert that the reported status is indeed stored in the addon's status
		// block.
		require.NotNil(t, addon.Status.ReportedStatus)
		require.Equal(t, addon.Spec.CorrelationID, addon.Status.ReportedStatus.CorrelationID)
		require.Equal(t, addon.Name, addon.Status.ReportedStatus.AddonID)
		require.Equal(t, mapAddonStatusConditions(addon.Status.Conditions),
			addon.Status.ReportedStatus.StatusConditions)
	})

	t.Run("Correctly patches OCM status with the current addon status", func(t *testing.T) {
		client := testutil.NewClient()
		ocmClient := ocmtest.NewClient()
		recorder := metrics.NewRecorder(false, "asa346546dfew143")
		mockSummary := testutil.NewSummaryMock()
		recorder.InjectAddonServiceAPIRequestDuration(mockSummary)
		statusReportingOption := &runtimeoptionstest.RuntimeOptionMock{}
		statusReportingOption.On("Enabled").Return(true)
		log := testutil.NewLogger(t)
		r := &AddonReconciler{
			Client:                client,
			ocmClient:             ocmClient,
			Recorder:              recorder,
			statusReportingOption: statusReportingOption,
		}

		addon := &addonsv1alpha1.Addon{
			ObjectMeta: metav1.ObjectMeta{
				Name: "addon-1",
			},
			Spec: addonsv1alpha1.AddonSpec{
				CorrelationID: "1234",
			},
			Status: addonsv1alpha1.AddonStatus{
				Conditions: []metav1.Condition{
					{
						Type:   addonsv1alpha1.Available,
						Status: metav1.ConditionTrue,
						Reason: addonsv1alpha1.AddonReasonFullyReconciled,
					},
					{
						Type:   addonsv1alpha1.UpgradeStarted,
						Status: metav1.ConditionTrue,
						Reason: addonsv1alpha1.AddonReasonUpgradeStarted,
					},
				},
				ReportedStatus: &addonsv1alpha1.OCMAddOnStatus{
					AddonID:       "addon-1",
					CorrelationID: "123",
					StatusConditions: []addonsv1alpha1.AddOnStatusCondition{
						{
							StatusType:  addonsv1alpha1.Available,
							StatusValue: metav1.ConditionFalse,
							Reason:      addonsv1alpha1.AddonReasonUnreadyCSV,
						},
					},
				},
			},
		}
		// setup mock calls
		ocmClient.On("GetAddOnStatus", mock.Anything, "addon-1").
			Return(
				ocm.AddOnStatusResponse{
					AddonID:       "addon-1",
					CorrelationID: "123",
					StatusConditions: []addonsv1alpha1.AddOnStatusCondition{
						{
							StatusType:  addonsv1alpha1.Available,
							StatusValue: metav1.ConditionFalse,
							Reason:      addonsv1alpha1.AddonReasonUnreadyCSV,
						},
					},
				},
				nil,
			)
		ocmClient.On("PatchAddOnStatus", mock.Anything, "addon-1", ocm.AddOnStatusPatchRequest{
			CorrelationID:    addon.Spec.CorrelationID,
			StatusConditions: mapAddonStatusConditions(addon.Status.Conditions),
		}).Return(
			ocm.AddOnStatusResponse{},
			nil,
		)
		mockSummary.On(
			"Observe", mock.IsType(float64(0)))

		err := r.handleOCMAddOnStatusReporting(context.Background(), log, addon)
		require.NoError(t, err)
		ocmClient.AssertExpectations(t)
		mockSummary.AssertExpectations(t)

		// Assert that the reported status is indeed stored in the addon's status
		// block.
		require.NotNil(t, addon.Status.ReportedStatus)
		require.Equal(t, addon.Spec.CorrelationID, addon.Status.ReportedStatus.CorrelationID)
		require.Equal(t, addon.Name, addon.Status.ReportedStatus.AddonID)
		require.Equal(t, mapAddonStatusConditions(addon.Status.Conditions),
			addon.Status.ReportedStatus.StatusConditions)
	})

	t.Run("errors are correctly returned and reported status is left untouched", func(t *testing.T) {
		client := testutil.NewClient()
		ocmClient := ocmtest.NewClient()
		recorder := metrics.NewRecorder(false, "asa346546dfew143")
		mockSummary := testutil.NewSummaryMock()
		recorder.InjectAddonServiceAPIRequestDuration(mockSummary)
		statusReportingOption := &runtimeoptionstest.RuntimeOptionMock{}
		statusReportingOption.On("Enabled").Return(true)
		log := testutil.NewLogger(t)
		r := &AddonReconciler{
			Client:                client,
			ocmClient:             ocmClient,
			Recorder:              recorder,
			statusReportingOption: statusReportingOption,
		}

		addon := &addonsv1alpha1.Addon{
			ObjectMeta: metav1.ObjectMeta{
				Name: "addon-1",
			},
			Spec: addonsv1alpha1.AddonSpec{
				CorrelationID: "1234",
			},
			Status: addonsv1alpha1.AddonStatus{
				Conditions: []metav1.Condition{
					{
						Type:   addonsv1alpha1.Available,
						Status: metav1.ConditionTrue,
						Reason: addonsv1alpha1.AddonReasonFullyReconciled,
					},
					{
						Type:   addonsv1alpha1.UpgradeStarted,
						Status: metav1.ConditionTrue,
						Reason: addonsv1alpha1.AddonReasonUpgradeStarted,
					},
				},
				ReportedStatus: &addonsv1alpha1.OCMAddOnStatus{
					AddonID:       "addon-1",
					CorrelationID: "123",
					StatusConditions: []addonsv1alpha1.AddOnStatusCondition{
						{
							StatusType:  addonsv1alpha1.Available,
							StatusValue: metav1.ConditionFalse,
							Reason:      addonsv1alpha1.AddonReasonUnreadyCSV,
						},
					},
				},
			},
		}
		originalReportedStatus := *addon.Status.ReportedStatus
		// setup mock calls
		ocmClient.On("GetAddOnStatus", mock.Anything, "addon-1").
			Return(
				ocm.AddOnStatusResponse{
					AddonID:       "addon-1",
					CorrelationID: "123",
					StatusConditions: []addonsv1alpha1.AddOnStatusCondition{
						{
							StatusType:  addonsv1alpha1.Available,
							StatusValue: metav1.ConditionFalse,
							Reason:      addonsv1alpha1.AddonReasonUnreadyCSV,
						},
					},
				},
				nil,
			)
		ocmClient.On("PatchAddOnStatus", mock.Anything, "addon-1", ocm.AddOnStatusPatchRequest{
			CorrelationID:    addon.Spec.CorrelationID,
			StatusConditions: mapAddonStatusConditions(addon.Status.Conditions),
		}).Return(
			ocm.AddOnStatusResponse{},
			ocm.OCMError{
				StatusCode: http.StatusGatewayTimeout,
			},
		)
		mockSummary.On(
			"Observe", mock.IsType(float64(0)))

		err := r.handleOCMAddOnStatusReporting(context.Background(), log, addon)
		require.Error(t, err)
		ocmClient.AssertExpectations(t)
		mockSummary.AssertExpectations(t)

		// Assert that the reported status is left unchanged because the reconciler
		// encountered an error.
		require.NotNil(t, addon.Status.ReportedStatus)
		require.Equal(t, originalReportedStatus, *addon.Status.ReportedStatus)
	})
}
