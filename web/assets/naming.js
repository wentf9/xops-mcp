export const nameHint =
  "允许各语言的字母或文字、数字、下划线和连字符，长度为 1–256 字节，不能包含空白或其他符号。";

const pattern = /^(?:\p{L}[\p{Mn}\p{Mc}]*|[\p{Nd}_-])+$/u;
const invisible = /\p{Default_Ignorable_Code_Point}/u;
const encoder = new TextEncoder();

export function validName(value) {
  return (
    typeof value === "string" &&
    encoder.encode(value).length <= 256 &&
    pattern.test(value) &&
    !invisible.test(value)
  );
}

export function aliasLines(value) {
  return String(value || "")
    .split(/\r?\n/)
    .filter((line) => line !== "");
}
