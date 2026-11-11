package nova

import (
	"context"
	"fmt"
	"io"
	"os"
)

// DiagnosisPackageFile is where DownloadDiagnosisPackage writes the package, in the
// working directory.
const DiagnosisPackageFile = "diagnosis.zip"

func downloadDiagnosisPackageV1Impl(host string) error {
	client, err := NewClientWithToken(host)
	if err != nil {
		return err
	}

	resp, err := client.GetDiagnosePackage(context.TODO())
	if err != nil {
		return err
	}

	if !StatusSuccessfull(resp.StatusCode) {
		return fmt.Errorf("failed to download diagnosis package: %s", resp.Status)
	}

	os.Remove(DiagnosisPackageFile)
	out, err := os.Create(DiagnosisPackageFile)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)

	return err
}
