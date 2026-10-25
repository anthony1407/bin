#!/usr/bin/env bash
# Console sign-in with a real directory and a real identity provider:
# OpenLDAP (users in or out of the admin group) and Keycloak (OpenID Connect,
# authorization code + PKCE, groups claim). The OpenID flow is driven like a
# browser: curl follows the redirects, fills Keycloak's login form and comes
# back to /oauth_callback with its own cookie jar. Root can always sign in.
source "$(dirname "$0")/lib.sh"
LDAP=bin-test-ldap
KC=bin-test-keycloak
LDAP_IMAGE=osixia/openldap:1.5.0
KC_IMAGE=quay.io/keycloak/keycloak:26.0.7
FIXTURES=$(native "$(cd "$(dirname "$0")/identity" && pwd)")

start_server
docker rm -f "$LDAP" "$KC" >/dev/null 2>&1
trap 'stop_server; docker rm -f "$LDAP" "$KC" >/dev/null 2>&1' EXIT
docker run -d --name "$LDAP" --network "$NET" -e LDAP_ORGANISATION=Example -e LDAP_DOMAIN=example.org \
  -e LDAP_ADMIN_PASSWORD=adminpw "$LDAP_IMAGE" >/dev/null || exit 1
docker run -d --name "$KC" --network "$NET" -v "$FIXTURES:/opt/keycloak/data/import:ro" \
  -e KC_BOOTSTRAP_ADMIN_USERNAME=admin -e KC_BOOTSTRAP_ADMIN_PASSWORD=admin \
  "$KC_IMAGE" start-dev --import-realm --http-port 8080 >/dev/null || exit 1
console_login

# --- LDAP ---------------------------------------------------------------------------

for _ in $(seq 1 30); do
  docker exec "$LDAP" ldapsearch -x -H ldap://localhost -b dc=example,dc=org -D cn=admin,dc=example,dc=org \
    -w adminpw -s base >/dev/null 2>&1 && break
  sleep 1
done
docker exec -i "$LDAP" ldapadd -x -H ldap://localhost -D cn=admin,dc=example,dc=org -w adminpw \
  <"$(dirname "$0")/identity/seed.ldif" >/dev/null || { echo "seeding LDAP failed" >&2; exit 1; }

LDAP_CFG='{"enabled":true,"serverAddr":"'"$LDAP"':389","tlsMode":"none","tlsSkipVerify":false,
  "lookupBindDn":"cn=admin,dc=example,dc=org","lookupBindPassword":"adminpw",
  "userDnSearchBase":"ou=people,dc=example,dc=org","userDnSearchFilter":"(&(objectClass=inetOrgPerson)(uid=%s))",
  "groupSearchBase":"ou=groups,dc=example,dc=org","groupSearchFilter":"(&(objectClass=groupOfNames)(member=%d))",
  "adminGroupDn":"cn=bin-admins,ou=groups,dc=example,dc=org"}'
check "ldap: test connection" '"status":"ok"' "$(api POST /identity/ldap/test "$LDAP_CFG")"
check "ldap: empty form refused, field named" '"field":"serverAddr"' "$(api PUT /identity/ldap '{"enabled":false}')"
SAVED=$(api PUT /identity/ldap "$LDAP_CFG")
check "ldap: save" "status=200" "$SAVED"
check "ldap: password never sent back" "absent" "$(echo "$SAVED" | grep -q adminpw && echo present || echo absent)"

# ldap_login <user> <password>: a fresh browser signing in with LDAP.
ldap_login() {
  docker run --rm --network "$NET" "$CURL_IMAGE" -s -w '\nstatus=%{http_code}' -H 'Content-Type: application/json' \
    --data '{"username":"'"$1"'","password":"'"$2"'"}' "http://$HOST:$CONSOLE_PORT/api/v1/login/ldap"
}
check "ldap: admin group member signs in" '"user":"ldap:ana"' "$(ldap_login ana ana-pass)"
check "ldap: non-member refused" '"error":"not_allowed"' "$(ldap_login luis luis-pass)"
check "ldap: wrong password" "status=401" "$(ldap_login ana wrong)"
check "ldap: empty password (anonymous bind) refused" "status=401" "$(ldap_login ana '')"
check "ldap: filter injection refused" "status=401" "$(ldap_login 'ana)(uid=*' ana-pass)"

# --- OpenID Connect (Keycloak) ------------------------------------------------------------

DISCOVERY="http://$KC:8080/realms/bin/.well-known/openid-configuration"
for _ in $(seq 1 120); do
  [ "$(curl_ -s -o /dev/null -w '%{http_code}' "$DISCOVERY")" = 200 ] && break
  sleep 2
done
check "keycloak is up" '"issuer":"http://'"$KC"':8080/realms/bin"' "$(curl_ -s "$DISCOVERY")"

OIDC_CFG='{"enabled":true,"configUrl":"'"$DISCOVERY"'","clientId":"bin-console","clientSecret":"bin-console-secret",
  "claimName":"groups","adminValue":"bin-admins","displayName":"Keycloak","scopes":"openid profile email","redirectUri":""}'
check "openid: test provider" '"issuer":"http://'"$KC"':8080/realms/bin"' "$(api POST /identity/openid/test '{"configUrl":"'"$DISCOVERY"'"}')"
SAVED=$(api PUT /identity/openid "$OIDC_CFG")
check "openid: save" '"clientSecretSet":true' "$SAVED"
check "openid: default redirect URI" '"defaultRedirectUri":"http://'"$HOST"':9001/oauth_callback"' "$SAVED"
check "login page offers both" '{"ldap":true,"openid":{"displayName":"Keycloak"}}' "$(curl_ -s "http://$HOST:$CONSOLE_PORT/api/v1/login/providers")"

# oidc_login <user> <password>: a browser going through Keycloak's login page.
# Prints the URL it ends on, then the session user (empty without a session).
cat >"$WORK/oidc-login.sh" <<'EOF'
set -e
J=/tmp/jar
page=$(curl -s -L -c $J -b $J "$BIN/api/v1/login/openid?next=/buckets/photos" | tr '\n' ' ')
action=$(printf '%s' "$page" | sed -n 's/.*id="kc-form-login"[^>]*action="\([^"]*\)".*/\1/p' | sed 's/&amp;/\&/g')
[ -n "$action" ] || { echo "no keycloak login form"; exit 1; }
curl -s -L -c $J -b $J -o /dev/null -w 'landed=%{url_effective}\n' \
  --data-urlencode "username=$USERNAME" --data-urlencode "password=$PASSWORD" "$action"
curl -s -b $J "$BIN/api/v1/session"
EOF
oidc_login() {
  docker run --rm --network "$NET" -v "$WORK_NATIVE:/work" -e BIN="http://$HOST:$CONSOLE_PORT" \
    -e USERNAME="$1" -e PASSWORD="$2" --entrypoint sh "$CURL_IMAGE" /work/oidc-login.sh 2>&1
}
ANA=$(oidc_login ana ana-pass)
check "openid: admin lands where it was going" "landed=http://$HOST:$CONSOLE_PORT/buckets/photos" "$ANA"
check "openid: admin gets a session" '"user":"openid:ana"' "$ANA"
LUIS=$(oidc_login luis luis-pass)
check "openid: non-member back at the login page" "/login?error=Your+account+is+not+allowed" "$LUIS"
check "openid: non-member gets no session" "not_signed_in" "$LUIS"
check "openid: forged callback refused" "/login?error=" \
  "$(curl_ -s -o /dev/null -w '%{redirect_url}' "http://$HOST:$CONSOLE_PORT/oauth_callback?code=x&state=y")"

check "sign-ins in the audit log" '"actor":"openid:ana","action":"LoginOpenID"' "$(api GET '/audit?q=openid')"
check "root can still sign in" "status=200" "$(api POST /login '{"accessKey":"'"$AK"'","secretKey":"'"$SK"'"}')"

finish
