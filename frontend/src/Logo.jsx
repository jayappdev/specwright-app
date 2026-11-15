// The app icon (build/appicon.svg) as an in-app mark.
import logo from "../../build/appicon.svg";

export function Logo({ size = 32, className = "", label }) {
  return (
    <img
      src={logo}
      width={size}
      height={size}
      className={"logo " + className}
      alt={label || ""}
      aria-hidden={label ? undefined : true}
      draggable={false}
    />
  );
}
