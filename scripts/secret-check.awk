BEGIN {
  field_re = "(^|[^a-z0-9])(api[_-]?key|api[_-]?token|client[_-]?secret|encryption[_-]?key|secret|passwo(r)?d|token)[[:space:]]*[:=]"
  dsn_re = "(postgres(ql)?|mysql|redis|mongodb(\\+srv)?):\\/\\/[^\\/@[:space:]\"']+:[^\\/@[:space:]\"']+@"
}

function whitelisted(value, lower) {
  if (value ~ /(REQFLOW|SILICONFLOW|MINERU|PINGCODE)_[A-Z_]+/) return 1
  if (length(value) >= 12 && value ~ /^[A-Z][A-Z_]+$/) return 1
  if (value ~ /^\$\{[A-Z_]+\}$/) return 1
  lower = tolower(value)
  if (lower ~ /your[-_]/ || lower ~ /changeme/ || lower ~ /placeholder/) return 1
  if (value ~ /^<[^>]+>$/) return 1
  if (value ~ /^([a-zA-Z][a-zA-Z0-9]*\.)+[a-zA-Z][a-zA-Z0-9]*$/) return 1
  if (value ~ /^&?[a-z][a-zA-Z0-9]*$/) return 1
  return 0
}

function has_dsn_secret(content, remaining, dsn, credentials, separator, username, password) {
  remaining = content
  while (match(remaining, dsn_re)) {
    dsn = substr(remaining, RSTART, RLENGTH)
    sub(/^[a-z+]+:\/\//, "", dsn)
    sub(/@$/, "", dsn)
    separator = index(dsn, ":")
    if (separator > 0) {
      username = substr(dsn, 1, separator - 1)
      password = substr(dsn, separator + 1)
      if (username != password) return 1
    }
    remaining = substr(remaining, RSTART + RLENGTH)
  }
  return 0
}

function has_field_secret(content, remaining, lower, field_end, value_text, value) {
  remaining = content
  while (match(tolower(remaining), field_re)) {
    field_end = RSTART + RLENGTH
    value_text = substr(remaining, field_end)
    sub(/^[[:space:]]+/, "", value_text)
    sub(/^[\"']/, "", value_text)
    if (match(value_text, /^[A-Za-z0-9+\/=_.:@~-]+/)) {
      value = substr(value_text, RSTART, RLENGTH)
      if (length(value) >= 12 && !whitelisted(value)) return 1
    }
    remaining = substr(remaining, field_end)
  }
  return 0
}

{
  first_separator = index($0, ":")
  if (first_separator == 0) next
  tail = substr($0, first_separator + 1)
  second_separator = index(tail, ":")
  if (second_separator == 0) next

  file = substr($0, 1, first_separator - 1)
  line_number = substr(tail, 1, second_separator - 1)
  content = substr(tail, second_separator + 1)
  if (has_dsn_secret(content) || has_field_secret(content)) {
    print file ":" line_number
    print "  " substr(content, 1, 120)
  }
}
