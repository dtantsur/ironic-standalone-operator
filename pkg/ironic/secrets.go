package ironic

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"maps"
	"math/big"
	"slices"
	"strings"
	"unicode"

	"github.com/go-logr/logr"
	"golang.org/x/crypto/bcrypt"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func checkValidUser(user string) error {
	for i, r := range user {
		if !unicode.IsLetter(r) && !unicode.IsNumber(r) && r != '-' && r != '_' {
			return fmt.Errorf("username cannot contain symbol %v (position %d)", r, i)
		}
	}
	return nil
}

// NOTE(dtantsur): this excludes most symbols that are hard to use in shell, or
// cause errors when substituting in SQL files, or are incompatible with
// the way MariaDB password is provided in ironic.conf. Make up for it by
// generating an absurdly long password.
const (
	passwordCharset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_"
	passwordLength  = 32
)

func generatePassword() ([]byte, error) {
	password := make([]byte, passwordLength)
	maxIdx := big.NewInt(int64(len(passwordCharset)))
	for i := range passwordLength {
		idx, err := rand.Int(rand.Reader, maxIdx)
		if err != nil {
			return nil, fmt.Errorf("cannot generate a new password: %w", err)
		}
		password[i] = passwordCharset[idx.Int64()]
	}

	return password, nil
}

func normalizeSecretValue(value []byte) string {
	return strings.Trim(string(value), " \n\r\t")
}

func generateHtpasswd(userBytes, passwordBytes []byte) (string, error) {
	// A common source of errors: an accidental line break after a password
	user := normalizeSecretValue(userBytes)
	password := normalizeSecretValue(passwordBytes)
	err := checkValidUser(user)
	if err != nil {
		return "", err
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("cannot generate a hashed password: %w", err)
	}

	return fmt.Sprintf("%s:%s", user, hashed), nil
}

func htpasswdFromSecret(secret *corev1.Secret) (string, error) {
	user, ok := secret.Data["username"]
	if !ok {
		return "", fmt.Errorf("missing username in secret %s/%s", secret.Namespace, secret.Name)
	}

	password, ok := secret.Data["password"]
	if !ok {
		return "", fmt.Errorf("missing password in secret %s/%s", secret.Namespace, secret.Name)
	}

	return generateHtpasswd(user, password)
}

const (
	htpasswdKey = "htpasswd"
)

func secretNeedsUpdating(secret *corev1.Secret, logger logr.Logger) bool {
	existing := secret.Data[htpasswdKey]
	user, password, ok := strings.Cut(string(existing), ":")
	if ok && user != "" && password != "" {
		newUser, ok := secret.Data[corev1.BasicAuthUsernameKey]
		newUserString := normalizeSecretValue(newUser)
		if ok && newUserString == user {
			newPassword, ok := secret.Data[corev1.BasicAuthPasswordKey]
			newPasswordString := normalizeSecretValue(newPassword)
			if ok && bcrypt.CompareHashAndPassword([]byte(password), []byte(newPasswordString)) == nil {
				// All good, keep the secret the way it is
				return false
			}
			logger.Info("API secret needs updating: passwords don't match")
		} else {
			logger.Info("API secret needs updating: users don't match", "HtpasswdUser", user, "CurrentUser", newUserString)
		}
	} else {
		logger.Info("API secret needs updating: no or malformed htpasswd")
	}

	return true
}

func UpdateSecret(secret *corev1.Secret, logger logr.Logger) (bool, error) {
	if !secretNeedsUpdating(secret, logger) {
		return false, nil
	}

	htpasswd, err := htpasswdFromSecret(secret)
	if err != nil {
		return false, err
	}
	secret.Data[htpasswdKey] = []byte(htpasswd)
	return true, nil
}

func GenerateSecret(owner *metav1.ObjectMeta, name string, extraFields bool) (*corev1.Secret, error) {
	pwd, err := generatePassword()
	if err != nil {
		return nil, err
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: fmt.Sprintf("%s-%s-", owner.Name, name),
			Namespace:    owner.Namespace,
		},
		Data: map[string][]byte{
			corev1.BasicAuthUsernameKey: []byte(owner.Name),
			corev1.BasicAuthPasswordKey: pwd,
		},
		Type: corev1.SecretTypeBasicAuth,
	}

	if extraFields {
		_, err = UpdateSecret(secret, logr.Discard())
		if err != nil {
			return nil, err
		}
	}

	return secret, nil
}

func secretVersionAnnotations(secretType string, secret *corev1.Secret) map[string]string {
	// Hash all data fields to detect changes
	h := fnv.New64a()

	// Sort keys for consistent hash ordering
	keys := slices.Sorted(maps.Keys(secret.Data))

	for _, k := range keys {
		h.Write([]byte(k))
		h.Write(secret.Data[k])
	}

	hash := hex.EncodeToString(h.Sum(nil))

	return map[string]string{
		fmt.Sprintf("ironic.metal3.io/%s-version", secretType): hash,
	}
}
